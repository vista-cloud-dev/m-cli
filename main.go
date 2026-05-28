// Command m is the cross-engine M toolchain (the busybox; spec §1). This stage
// ships `m fmt` — the AST-preserving formatter over the m-parse substrate
// (spec §3.1) — alongside the shared clikit conventions (--output text|json,
// schema, deterministic errors). lint/lsp/test/coverage/watch land next.
//
// Try:
//
//	m fmt routine.m                      # report whether it needs formatting
//	m fmt --rules=canonical --write .    # uppercase command keywords in place
//	m fmt --rules=canonical --check .    # CI gate: exit 3 if any file differs
//	cat routine.m | m fmt --rules=canonical --stdin
//	m schema | jq .
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/willabides/kongplete"

	"github.com/vista-cloud-dev/m-cli/clikit"
	"github.com/vista-cloud-dev/m-cli/internal/mfmt"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// CLI is the root command grammar (one typed struct; spec §5).
type CLI struct {
	clikit.Globals

	Fmt     fmtCmd           `cmd:"" help:"Format M source over the parse tree (AST-preserving)."`
	Version versionCmd       `cmd:"" help:"Show version, Go toolchain, and embedded grammar hash."`
	Schema  clikit.SchemaCmd `cmd:"" help:"Emit the command/flag/enum tree as JSON (agent discovery)."`

	InstallCompletions kongplete.InstallCompletions `cmd:"" help:"Install shell tab-completions."`
}

func main() {
	cli := &CLI{}
	os.Exit(clikit.Run(
		"m",
		"m — the cross-engine M toolchain (fmt/lint/lsp/test/coverage/watch over YottaDB and IRIS).",
		cli, &cli.Globals,
	))
}

// --- fmt ---------------------------------------------------------------------

type fmtCmd struct {
	Paths []string `arg:"" optional:"" type:"path" help:"Files or directories to format (default: .)."`
	Rules string   `default:"identity" enum:"identity,canonical" help:"Rule preset: identity (no-op) or canonical."`
	Check bool     `help:"Report files needing formatting; exit 3 if any (no writes)."`
	Write bool     `short:"w" help:"Rewrite changed files in place."`
	Stdin bool     `help:"Format stdin → stdout (raw; ignores paths and --output)."`
}

type fmtResult struct {
	Rules   string   `json:"rules"`
	Scanned int      `json:"scanned"`
	Changed []string `json:"changed"`
	Wrote   []string `json:"wrote,omitempty"`
}

func (c *fmtCmd) Run(cc *clikit.Context) error {
	ctx := context.Background()
	p, err := parse.New(ctx)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "PARSER_INIT", err.Error(), "")
	}
	defer func() { _ = p.Close(ctx) }()

	rules := mfmt.Rules(mfmt.Preset(c.Rules))

	// --stdin: behave as a filter — raw formatted bytes to stdout.
	if c.Stdin {
		src, err := io.ReadAll(os.Stdin)
		if err != nil {
			return clikit.Fail(clikit.ExitRuntime, "READ_FAILED", err.Error(), "")
		}
		out, err := mfmt.Format(ctx, p, src, rules)
		if err != nil {
			return clikit.Fail(clikit.ExitRuntime, "FORMAT_FAILED", err.Error(), "")
		}
		_, _ = cc.Stdout.Write(out)
		return nil
	}

	paths := c.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}
	files, err := discover(paths)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "DISCOVER_FAILED", err.Error(), "")
	}

	res := fmtResult{Rules: c.Rules, Scanned: len(files)}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return clikit.Fail(clikit.ExitRuntime, "READ_FAILED", fmt.Sprintf("%s: %v", f, err), "")
		}
		out, err := mfmt.Format(ctx, p, src, rules)
		if err != nil {
			return clikit.Fail(clikit.ExitRuntime, "FORMAT_FAILED", fmt.Sprintf("%s: %v", f, err), "")
		}
		if string(out) == string(src) {
			continue
		}
		res.Changed = append(res.Changed, f)
		if c.Write {
			info, statErr := os.Stat(f)
			mode := os.FileMode(0o644)
			if statErr == nil {
				mode = info.Mode().Perm()
			}
			if err := os.WriteFile(f, out, mode); err != nil {
				return clikit.Fail(clikit.ExitRuntime, "WRITE_FAILED", fmt.Sprintf("%s: %v", f, err), "")
			}
			res.Wrote = append(res.Wrote, f)
		}
	}

	// --check: fail (exit 3) if anything would change, with a clean error.
	if c.Check && len(res.Changed) > 0 {
		return clikit.Fail(clikit.ExitCheck, "UNFORMATTED",
			fmt.Sprintf("%d of %d file(s) need formatting: %s",
				len(res.Changed), res.Scanned, strings.Join(res.Changed, ", ")),
			"run with --write to apply")
	}

	return cc.Result(res, func() {
		cc.Title("fmt")
		cc.KV(
			[2]string{"rules", cc.Accent(res.Rules)},
			[2]string{"scanned", fmt.Sprintf("%d", res.Scanned)},
			[2]string{"changed", fmt.Sprintf("%d", len(res.Changed))},
		)
		if c.Write && len(res.Wrote) > 0 {
			fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("formatted %d file(s)", len(res.Wrote))))
		}
		for _, f := range res.Changed {
			verb := "needs formatting"
			if c.Write {
				verb = "formatted"
			}
			fmt.Fprintf(cc.Stdout, "  %s %s  %s\n", cc.Faint(cc.Glyphs().Arrow), f, cc.Faint(verb))
		}
		if len(res.Changed) == 0 {
			fmt.Fprintln(cc.Stdout, cc.Success("all files already formatted"))
		}
	})
}

// discover expands paths into M source files. Directories are walked for
// .m/.mac/.int (VistA loaded via ^%RI stores routine source as .int, so it is
// included alongside YDB .m and IRIS .mac); explicit file args are kept as-is.
func discover(paths []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			add(root)
			continue
		}
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "dist", "vendor", "node_modules":
					return filepath.SkipDir
				}
				return nil
			}
			if isMFile(path) {
				add(path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func isMFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m", ".mac", ".int":
		return true
	}
	return false
}

// --- version -----------------------------------------------------------------

type versionCmd struct{}

type versionInfo struct {
	Version     string `json:"version"`
	Commit      string `json:"commit"`
	Date        string `json:"date"`
	Go          string `json:"go"`
	GrammarHash string `json:"grammarHash"`
}

func (versionCmd) Run(cc *clikit.Context) error {
	info := versionInfo{
		Version: clikit.Version, Commit: clikit.Commit, Date: clikit.Date,
		Go: runtime.Version(), GrammarHash: parse.GrammarHash(),
	}
	return cc.Result(info, func() {
		cc.KV(
			[2]string{"version", cc.Accent(info.Version)},
			[2]string{"commit", info.Commit},
			[2]string{"built", info.Date},
			[2]string{"go", info.Go},
			[2]string{"grammar", info.GrammarHash},
		)
	})
}
