package dispatch

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/m-cli/clikit"
)

// fakeBin writes an executable shell script to dir/name and returns its path.
// The script echoes its argv (one per line, prefixed) and stdin, then exits
// with the given code — enough to assert forwarding without a real sibling.
func fakeBin(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// argvEcho is a sibling that prints each argument on its own line and exits 7.
const argvEcho = `for a in "$@"; do echo "ARG:$a"; done
exit 7
`

func TestResolveEnvOverride(t *testing.T) {
	dir := t.TempDir()
	want := fakeBin(t, dir, "irissync", "exit 0\n")
	t.Setenv("M_IRISSYNC_BIN", want)

	got, err := Resolve("irissync")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != want {
		t.Errorf("Resolve = %q, want %q", got, want)
	}
}

func TestResolveNotFound(t *testing.T) {
	t.Setenv("M_NOSUCHTOOL_BIN", "")
	_, err := Resolve("nosuchtool-xyzzy")
	if err == nil {
		t.Fatal("Resolve: want error for missing binary")
	}
	var ce *clikit.Error
	if !errors.As(err, &ce) {
		t.Fatalf("Resolve err = %T, want *clikit.Error", err)
	}
	if ce.Code != "SIBLING_NOT_FOUND" || ce.Exit != clikit.ExitRuntime {
		t.Errorf("Resolve err = {code:%q exit:%d}, want {SIBLING_NOT_FOUND %d}", ce.Code, ce.Exit, clikit.ExitRuntime)
	}
	if !strings.Contains(ce.Hint, "M_NOSUCHTOOL_XYZZY_BIN") {
		t.Errorf("hint should name the override env var, got %q", ce.Hint)
	}
}

func TestEnvVar(t *testing.T) {
	if got := envVar("kids-vc"); got != "M_KIDS_VC_BIN" {
		t.Errorf("envVar(kids-vc) = %q, want M_KIDS_VC_BIN", got)
	}
}

func TestRunFlatForwardsGlobalsPrefixArgsAndExit(t *testing.T) {
	dir := t.TempDir()
	fakeBin(t, dir, "irissync", argvEcho)
	t.Setenv("M_IRISSYNC_BIN", filepath.Join(dir, "irissync"))

	spec, _ := Find("pull") // flat: prefix is [pull]
	var out, errOut bytes.Buffer
	code, err := Run(context.Background(), spec,
		[]string{"--output", "json"}, []string{"--force", "R1"},
		strings.NewReader(""), &out, &errOut)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7 (forwarded from child)", code)
	}
	got := out.String()
	want := "ARG:--output\nARG:json\nARG:pull\nARG:--force\nARG:R1\n"
	if got != want {
		t.Errorf("forwarded argv:\n got %q\nwant %q", got, want)
	}
}

func TestRunGroupForwardsRestVerbatim(t *testing.T) {
	dir := t.TempDir()
	fakeBin(t, dir, "kids-vc", argvEcho)
	t.Setenv("M_KIDS_VC_BIN", filepath.Join(dir, "kids-vc"))

	spec, _ := Find("kids") // group: no prefix; rest carries the subcommand
	var out bytes.Buffer
	code, err := Run(context.Background(), spec,
		nil, []string{"decompose", "x.KID"},
		strings.NewReader(""), &out, &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if code != 7 {
		t.Errorf("exit code = %d, want 7", code)
	}
	if got, want := out.String(), "ARG:decompose\nARG:x.KID\n"; got != want {
		t.Errorf("group argv:\n got %q\nwant %q", got, want)
	}
}

func TestRunStdinPassthrough(t *testing.T) {
	dir := t.TempDir()
	fakeBin(t, dir, "kids-vc", "cat\nexit 0\n")
	t.Setenv("M_KIDS_VC_BIN", filepath.Join(dir, "kids-vc"))

	spec, _ := Find("kids")
	var out bytes.Buffer
	_, err := Run(context.Background(), spec, nil, nil,
		strings.NewReader("hello-stdin"), &out, &out)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); got != "hello-stdin" {
		t.Errorf("stdin passthrough = %q, want hello-stdin", got)
	}
}

func TestRunMissingBinaryIsDeterministic(t *testing.T) {
	t.Setenv("M_IRISSYNC_BIN", filepath.Join(t.TempDir(), "does-not-exist"))
	spec, _ := Find("pull")
	_, err := Run(context.Background(), spec, nil, nil,
		strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	var ce *clikit.Error
	if !errors.As(err, &ce) || ce.Code != "SIBLING_NOT_FOUND" {
		t.Fatalf("Run missing binary err = %v, want *clikit.Error SIBLING_NOT_FOUND", err)
	}
}

// A fake sibling that emits a clikit schema when asked, else echoes argv.
func schemaFake(tool string, cmds ...string) string {
	var b strings.Builder
	b.WriteString(`if [ "$1" = schema ]; then cat <<'JSON'` + "\n")
	b.WriteString(`{"schemaVersion":"1.0","tool":"` + tool + `","version":"0.0.0","commands":[`)
	for i, c := range cmds {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"path":["` + c + `"],"help":"` + c + ` help","flags":[{"name":"force","type":"bool"}]}`)
	}
	b.WriteString("]}\nJSON\nexit 0\nfi\n")
	b.WriteString(argvEcho)
	return b.String()
}

func TestAggregateGraftsSiblings(t *testing.T) {
	dir := t.TempDir()
	fakeBin(t, dir, "irissync", schemaFake("irissync", "list", "pull", "status", "verify", "push"))
	fakeBin(t, dir, "kids-vc", schemaFake("kids-vc", "decompose", "assemble", "lint"))
	t.Setenv("M_IRISSYNC_BIN", filepath.Join(dir, "irissync"))
	t.Setenv("M_KIDS_VC_BIN", filepath.Join(dir, "kids-vc"))

	// Native doc with passthrough stubs for the dispatched namespaces.
	doc := clikit.SchemaDoc{
		SchemaVersion: "1.0", Tool: "m", Version: "1.0",
		Commands: []clikit.SchemaCommand{
			{Path: []string{"fmt"}},
			{Path: []string{"pull"}, Args: []clikit.SchemaArg{{Name: "rest"}}},
			{Path: []string{"push"}, Args: []clikit.SchemaArg{{Name: "rest"}}},
			{Path: []string{"list"}, Args: []clikit.SchemaArg{{Name: "rest"}}},
			{Path: []string{"status"}, Args: []clikit.SchemaArg{{Name: "rest"}}},
			{Path: []string{"verify"}, Args: []clikit.SchemaArg{{Name: "rest"}}},
			{Path: []string{"kids"}, Args: []clikit.SchemaArg{{Name: "rest"}}},
		},
	}
	out := Aggregate(context.Background(), doc)

	byPath := map[string]clikit.SchemaCommand{}
	for _, c := range out.Commands {
		byPath[strings.Join(c.Path, " ")] = c
	}
	// fmt untouched.
	if _, ok := byPath["fmt"]; !ok {
		t.Error("native fmt command was dropped")
	}
	// Flat verb grafted with the sibling's real flags (not the stub `rest` arg).
	pull, ok := byPath["pull"]
	if !ok {
		t.Fatal("pull not present after aggregate")
	}
	if len(pull.Args) != 0 || len(pull.Flags) != 1 || pull.Flags[0].Name != "force" {
		t.Errorf("pull not grafted from sibling schema: %+v", pull)
	}
	// Group subcommands grafted under [kids ...].
	if _, ok := byPath["kids decompose"]; !ok {
		t.Error("kids decompose not grafted under kids namespace")
	}
	if _, ok := byPath["kids lint"]; !ok {
		t.Error("kids lint not grafted under kids namespace")
	}
	// The bare `kids` stub should be gone (replaced by real subcommands).
	if _, ok := byPath["kids"]; ok {
		t.Error("bare kids stub should be replaced by grafted subcommands")
	}
}

func TestAggregateMissingSiblingsKeepsStubs(t *testing.T) {
	// Point overrides at nonexistent paths so every sibling fails to resolve.
	miss := filepath.Join(t.TempDir(), "nope")
	t.Setenv("M_IRISSYNC_BIN", miss)
	t.Setenv("M_KIDS_VC_BIN", miss)

	doc := clikit.SchemaDoc{
		SchemaVersion: "1.0", Tool: "m", Version: "1.0",
		Commands: []clikit.SchemaCommand{
			{Path: []string{"pull"}, Args: []clikit.SchemaArg{{Name: "rest"}}},
			{Path: []string{"kids"}, Args: []clikit.SchemaArg{{Name: "rest"}}},
		},
	}
	out := Aggregate(context.Background(), doc)
	if len(out.Commands) != 2 {
		t.Fatalf("missing siblings: want stubs preserved (2 commands), got %d", len(out.Commands))
	}
}
