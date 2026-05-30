// Package dispatch is the `m` busybox's sibling-binary dispatcher (spec §2.2,
// tracker [N12]). `m` keeps its native subcommands (fmt/lint/lsp/test/coverage/
// watch) and forwards the rest to standalone sibling binaries — keeping each
// sibling's own SBOM and release cadence (a small attestable family, ADR §5)
// rather than one mixed-dep blob. Two dispatch shapes are supported:
//
//   - flat: one `m` verb maps to the sibling command of the same name
//     (`m pull` → `irissync pull`).
//   - group: one `m` verb forwards the remaining args verbatim to the sibling
//     (`m kids decompose x.KID` → `kids-vc decompose x.KID`).
//
// Discovery, exit-code forwarding, and schema aggregation are all deterministic
// (the §3.3 ladder) so agents and CI compose `m` exactly as they compose the
// siblings directly.
package dispatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/vista-cloud-dev/m-cli/clikit"
)

// Spec describes one dispatched sibling namespace.
type Spec struct {
	// Verb is the `m` subcommand token (e.g. "pull", "kids").
	Verb string
	// Binary is the sibling executable name (e.g. "irissync", "kids-vc").
	Binary string
	// Group is false for a flat verb (the verb is the sibling subcommand) and
	// true for a group (the forwarded args carry the sibling subcommand).
	Group bool
}

// Registry is the active dispatch table.
//
// Extension point (the deferred 2.2 `m meta` hook, tracker [N14]): to front
// vista-meta as `m meta …`, append a flat-or-group Spec here once that binary
// exists — e.g. {Verb: "meta", Binary: "vista-meta", Group: true}. No other
// change is needed: discovery, forwarding, and `m schema` aggregation all key
// off this slice. Likewise for `m mcp` → m-dev-tools-mcp (5.2). Nothing is
// wired for either today, by design.
var Registry = []Spec{
	{Verb: "list", Binary: "irissync"},
	{Verb: "pull", Binary: "irissync"},
	{Verb: "status", Binary: "irissync"},
	{Verb: "verify", Binary: "irissync"},
	{Verb: "push", Binary: "irissync"},
	{Verb: "kids", Binary: "kids-vc", Group: true},
}

// notForwarded are sibling commands that are `m`'s own surface, not part of a
// dispatched namespace, so `m schema` must not graft them from a sibling.
var notForwarded = map[string]bool{
	"schema": true, "version": true, "install-completions": true,
}

// Find returns the Spec for a `m` verb.
func Find(verb string) (Spec, bool) {
	for _, s := range Registry {
		if s.Verb == verb {
			return s, true
		}
	}
	return Spec{}, false
}

// Verbs lists every dispatched `m` verb, in registry order.
func Verbs() []string {
	out := make([]string, len(Registry))
	for i, s := range Registry {
		out[i] = s.Verb
	}
	return out
}

// envVar is the per-binary discovery override, e.g. kids-vc → M_KIDS_VC_BIN.
func envVar(binary string) string {
	up := strings.ToUpper(strings.NewReplacer("-", "_", ".", "_").Replace(binary))
	return "M_" + up + "_BIN"
}

// Resolve locates a sibling binary: the M_<NAME>_BIN override first, then
// alongside the running `m`, then $PATH. A miss is a deterministic error, never
// a panic or a raw exec failure.
func Resolve(binary string) (string, error) {
	if override := os.Getenv(envVar(binary)); override != "" {
		if isExecutable(override) {
			return override, nil
		}
		return "", notFound(binary, override)
	}
	if self, err := os.Executable(); err == nil {
		cand := filepath.Join(filepath.Dir(self), binary)
		if isExecutable(cand) {
			return cand, nil
		}
	}
	if p, err := exec.LookPath(binary); err == nil {
		return p, nil
	}
	return "", notFound(binary, "")
}

func isExecutable(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

func notFound(binary, tried string) error {
	msg := fmt.Sprintf("dispatched binary %q not found", binary)
	if tried != "" {
		msg = fmt.Sprintf("dispatched binary %q not found at %s", binary, tried)
	}
	hint := fmt.Sprintf("install %q alongside m or on $PATH, or set %s=/path/to/%s",
		binary, envVar(binary), binary)
	return clikit.Fail(clikit.ExitRuntime, "SIBLING_NOT_FOUND", msg, hint)
}

// Run resolves spec.Binary and execs it, forwarding the resolved global flags,
// the sibling subcommand (for a flat verb), and the user's args, with the child
// inheriting the given stdio. It returns the child's exit code faithfully, or a
// deterministic *clikit.Error when the binary can't be resolved or spawned.
func Run(ctx context.Context, spec Spec, globals, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	bin, err := Resolve(spec.Binary)
	if err != nil {
		return 0, err
	}
	childArgs := append([]string{}, globals...)
	if !spec.Group {
		childArgs = append(childArgs, spec.Verb)
	}
	childArgs = append(childArgs, args...)

	cmd := exec.CommandContext(ctx, bin, childArgs...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	switch err := cmd.Run(); {
	case err == nil:
		return clikit.ExitOK, nil
	case isExitError(err):
		var ee *exec.ExitError
		errors.As(err, &ee)
		return ee.ExitCode(), nil
	default:
		return 0, clikit.Fail(clikit.ExitRuntime, "DISPATCH_FAILED",
			fmt.Sprintf("failed to run %q: %v", spec.Binary, err), "")
	}
}

func isExitError(err error) bool {
	var ee *exec.ExitError
	return errors.As(err, &ee)
}

// Aggregate merges each available sibling's sub-schema into doc so an agent
// sees one tree (spec §2.2). For each registered namespace it execs `<binary>
// schema`, then grafts the sibling's commands in place of the native stub:
// a flat verb keeps its top-level path; a group's subcommands attach under
// [verb …]. A sibling that can't be resolved (e.g. the CI schema-contract runs
// with no siblings present) or whose schema can't be read is left as its stub,
// so the output is always valid JSON and every namespace stays discoverable.
func Aggregate(ctx context.Context, doc clikit.SchemaDoc) clikit.SchemaDoc {
	subs := siblingSchemas(ctx) // binary → its schema (only those that resolved)

	// Partition: which top-level verbs are dispatched, and which resolved.
	grafted := map[string]bool{} // verbs whose sibling schema we will graft

	var out []clikit.SchemaCommand
	// Keep native (non-dispatched) commands, plus stubs for verbs we can't graft.
	for _, c := range doc.Commands {
		if len(c.Path) == 0 {
			out = append(out, c)
			continue
		}
		spec, isDispatch := Find(c.Path[0])
		if !isDispatch || len(c.Path) > 1 {
			out = append(out, c) // native command (or already-grafted subpath)
			continue
		}
		if _, ok := subs[spec.Binary]; ok {
			grafted[spec.Verb] = true // drop the stub; real commands added below
			continue
		}
		out = append(out, c) // sibling unavailable → keep the stub
	}

	// Graft the resolved siblings' commands into the dispatched namespaces.
	for _, spec := range Registry {
		if !grafted[spec.Verb] {
			continue
		}
		for _, sc := range subs[spec.Binary].Commands {
			if len(sc.Path) == 0 || notForwarded[sc.Path[0]] {
				continue
			}
			if spec.Group {
				sc.Path = append([]string{spec.Verb}, sc.Path...)
				out = append(out, sc)
			} else if sc.Path[0] == spec.Verb {
				out = append(out, sc) // flat: path unchanged
			}
		}
	}

	doc.Commands = out
	return doc
}

// siblingSchemas execs `schema` on each registered binary once, returning the
// parsed sub-schemas for those that resolve and emit valid JSON.
func siblingSchemas(ctx context.Context) map[string]clikit.SchemaDoc {
	out := map[string]clikit.SchemaDoc{}
	for _, spec := range Registry {
		if _, done := out[spec.Binary]; done {
			continue
		}
		if sub, err := SiblingSchema(ctx, spec.Binary); err == nil {
			out[spec.Binary] = sub
		}
	}
	return out
}

// SiblingSchema resolves a binary and returns its reflected schema.
func SiblingSchema(ctx context.Context, binary string) (clikit.SchemaDoc, error) {
	bin, err := Resolve(binary)
	if err != nil {
		return clikit.SchemaDoc{}, err
	}
	cmd := exec.CommandContext(ctx, bin, "schema")
	stdout, err := cmd.Output()
	if err != nil {
		return clikit.SchemaDoc{}, fmt.Errorf("%s schema: %w", binary, err)
	}
	var doc clikit.SchemaDoc
	if err := json.Unmarshal(stdout, &doc); err != nil {
		return clikit.SchemaDoc{}, fmt.Errorf("%s schema: %w", binary, err)
	}
	return doc, nil
}
