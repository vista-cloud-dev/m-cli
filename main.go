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
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/willabides/kongplete"

	"github.com/vista-cloud-dev/m-cli/clikit"
	"github.com/vista-cloud-dev/m-cli/internal/engine"
	"github.com/vista-cloud-dev/m-cli/internal/lint"
	"github.com/vista-cloud-dev/m-cli/internal/lsp"
	"github.com/vista-cloud-dev/m-cli/internal/mfmt"
	"github.com/vista-cloud-dev/m-cli/internal/mtest"
	"github.com/vista-cloud-dev/m-cli/internal/watch"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// CLI is the root command grammar (one typed struct; spec §5).
type CLI struct {
	clikit.Globals

	Fmt     fmtCmd           `cmd:"" help:"Format M source over the parse tree (AST-preserving)."`
	Lint    lintCmd          `cmd:"" help:"Lint M source over the parse tree (query-driven rules)."`
	Lsp     lspCmd           `cmd:"" help:"Run the M language server (LSP 3.x over stdio)."`
	Test    testCmd          `cmd:"" help:"Run *TST.m suites through the engine (^STDASSERT)."`
	Watch   watchCmd         `cmd:"" help:"Re-run lint (and fmt-check) on M files as they change (static half)."`
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

// --- lint --------------------------------------------------------------------

type lintCmd struct {
	Paths     []string `arg:"" optional:"" type:"path" help:"Files or directories to lint (default: .)."`
	Profile   string   `default:"default" enum:"default,modern,all" help:"Rule profile."`
	Check     bool     `help:"Exit 3 if there are any findings (CI gate)."`
	ListRules bool     `help:"List the rules in the selected profile (then exit)."`
}

type ruleDoc struct {
	ID       string   `json:"id"`
	Severity string   `json:"severity"`
	Profiles []string `json:"profiles"`
	Doc      string   `json:"doc"`
}

func (c *lintCmd) Run(cc *clikit.Context) error {
	rules := lint.Profile(c.Profile)

	if c.ListRules {
		docs := make([]ruleDoc, 0, len(rules))
		for _, r := range rules {
			docs = append(docs, ruleDoc{ID: r.ID, Severity: string(r.Severity), Profiles: r.Profiles, Doc: r.Doc})
		}
		return cc.Result(docs, func() {
			cc.Title("lint rules — profile " + c.Profile)
			for _, d := range docs {
				fmt.Fprintf(cc.Stdout, "  %s  %s  %s\n", cc.Accent(d.ID), cc.Faint(d.Severity), d.Doc)
			}
		})
	}

	ctx := context.Background()
	p, err := parse.New(ctx)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "PARSER_INIT", err.Error(), "")
	}
	defer func() { _ = p.Close(ctx) }()

	linter, err := lint.NewLinter(p, rules)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "RULE_COMPILE", err.Error(), "")
	}
	defer linter.Close()

	paths := c.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}
	files, err := discover(paths)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "DISCOVER_FAILED", err.Error(), "")
	}

	var diags []clikit.Diagnostic
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return clikit.Fail(clikit.ExitRuntime, "READ_FAILED", fmt.Sprintf("%s: %v", f, err), "")
		}
		findings, err := linter.Lint(ctx, src)
		if err != nil {
			return clikit.Fail(clikit.ExitRuntime, "LINT_FAILED", fmt.Sprintf("%s: %v", f, err), "")
		}
		for _, fd := range findings {
			diags = append(diags, clikit.Diagnostic{
				File: f, Line: fd.Line, Col: fd.Col,
				Rule: fd.Rule, Severity: string(fd.Severity), Message: fd.Message,
			})
		}
	}

	summary := map[string]int{"filesScanned": len(files), "findings": len(diags)}
	if err := cc.Diagnostics(summary, diags, func() {
		cc.Title("lint")
		for _, d := range diags {
			fmt.Fprintf(cc.Stdout, "%s  %s:%d:%d  %s  %s\n",
				cc.Severity(d.Severity), d.File, d.Line, d.Col, cc.Faint(d.Rule), d.Message)
		}
		if len(diags) == 0 {
			fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("no findings (%d files, profile %s)", len(files), c.Profile)))
		} else {
			fmt.Fprintln(cc.Stdout, cc.Faint(fmt.Sprintf("%d finding(s) in %d file(s)", len(diags), len(files))))
		}
	}); err != nil {
		return err
	}
	if c.Check && len(diags) > 0 {
		return clikit.Fail(clikit.ExitCheck, "FINDINGS",
			fmt.Sprintf("%d lint finding(s) in %d file(s)", len(diags), len(files)), "")
	}
	return nil
}

// --- test --------------------------------------------------------------------

type testCmd struct {
	Paths    []string `arg:"" optional:"" type:"path" help:"Suites or directories to run (default: .)."`
	Engine   string   `help:"Engine: ydb or iris. Else $M_ENGINE / heuristic; refuses (exit 4) if unresolved."`
	Docker   string   `help:"Run inside this running container via docker exec (e.g. m-test-engine)."`
	Routines []string `help:"Extra source dirs to stage (e.g. m-stdlib/src for ^STDASSERT). Repeatable."`
}

type suiteResult struct {
	Suite  string `json:"suite"`
	Passed int    `json:"passed"`
	Failed int    `json:"failed"`
	Total  int    `json:"total"`
	OK     bool   `json:"ok"`
}

type testReport struct {
	Engine  string        `json:"engine"`
	Suites  int           `json:"suites"`
	Passed  int           `json:"passed"`
	Failed  int           `json:"failed"`
	Results []suiteResult `json:"results"`
}

func (c *testCmd) Run(cc *clikit.Context) error {
	// Engine-bound command: resolve the engine and refuse (exit 4) if the
	// choice is the bare default (§2.1 ambiguity rule).
	kind, explicit, err := engine.Resolve(engine.DetectConfig(c.Engine))
	if err != nil {
		return clikit.Fail(clikit.ExitUsage, "BAD_ENGINE", err.Error(), "use --engine ydb|iris")
	}
	if !explicit {
		return clikit.Fail(clikit.ExitRefused, "ENGINE_UNRESOLVED",
			"no engine resolved for an engine-bound command",
			"pass --engine ydb|iris, set $M_ENGINE, or run where IRIS is detected")
	}

	ctx := context.Background()
	p, err := parse.New(ctx)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "PARSER_INIT", err.Error(), "")
	}
	defer func() { _ = p.Close(ctx) }()

	paths := c.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}
	suites, err := mtest.Discover(p, paths)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "DISCOVER_FAILED", err.Error(), "")
	}

	report := testReport{Engine: string(kind), Suites: len(suites)}
	var failedSuites int
	if len(suites) > 0 {
		var eng engine.Engine
		if c.Docker != "" {
			// Stage the suites (+ any --routines deps like ^STDASSERT) into a
			// scratch dir in the container and run there via docker exec.
			stageDir := fmt.Sprintf("/m-work/m-test-%d", time.Now().UnixNano())
			var files []string
			for _, s := range suites {
				files = append(files, s.Path)
			}
			for _, rdir := range c.Routines {
				ms, _ := filepath.Glob(filepath.Join(rdir, "*.m"))
				files = append(files, ms...)
			}
			if err := engine.DockerStage(ctx, c.Docker, stageDir, files); err != nil {
				return clikit.Fail(clikit.ExitRuntime, "STAGE_FAILED", err.Error(), "")
			}
			defer engine.DockerUnstage(ctx, c.Docker, stageDir)
			eng = engine.New(kind, engine.Options{Runner: engine.DockerRunner(c.Docker, stageDir)})
		} else {
			eng = engine.New(kind, engine.Options{})
		}
		results, runErr := mtest.Run(ctx, eng, suites)
		if runErr != nil {
			return clikit.Fail(clikit.ExitRuntime, "ENGINE_RUN", runErr.Error(),
				"m test runs on a live engine — is ydb/iris installed and reachable?")
		}
		for _, r := range results {
			report.Passed += r.Summary.Passed
			report.Failed += r.Summary.Failed
			if !r.OK {
				failedSuites++
			}
			report.Results = append(report.Results, suiteResult{
				Suite: r.Suite, Passed: r.Summary.Passed, Failed: r.Summary.Failed,
				Total: r.Summary.Total, OK: r.OK,
			})
		}
	}

	if err := cc.Result(report, func() {
		cc.Title("test")
		cc.KV(
			[2]string{"engine", cc.Accent(report.Engine)},
			[2]string{"suites", fmt.Sprintf("%d", report.Suites)},
			[2]string{"assertions", fmt.Sprintf("%d passed, %d failed", report.Passed, report.Failed)},
		)
		for _, r := range report.Results {
			mark := cc.Success("PASS")
			if !r.OK {
				mark = cc.Failure("FAIL")
			}
			fmt.Fprintf(cc.Stdout, "  %s %s  %s\n", mark, r.Suite,
				cc.Faint(fmt.Sprintf("%d/%d passed", r.Passed, r.Total)))
		}
		if report.Suites == 0 {
			fmt.Fprintln(cc.Stdout, cc.Faint("no *TST.m suites found"))
		}
	}); err != nil {
		return err
	}

	if failedSuites > 0 {
		return clikit.Fail(clikit.ExitCheck, "TESTS_FAILED",
			fmt.Sprintf("%d of %d suite(s) failed", failedSuites, report.Suites), "")
	}
	return nil
}

// --- watch -------------------------------------------------------------------

type watchCmd struct {
	Paths    []string `arg:"" optional:"" type:"path" help:"Files or directories to watch (default: .)."`
	Profile  string   `default:"default" enum:"default,modern,all" help:"Lint rule profile."`
	Interval int      `default:"500" help:"Poll interval in milliseconds."`
	Fmt      bool     `help:"Also flag files that aren't canonically formatted."`
}

func (c *watchCmd) Run(cc *clikit.Context) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	p, err := parse.New(ctx)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "PARSER_INIT", err.Error(), "")
	}
	defer func() { _ = p.Close(context.Background()) }()

	linter, err := lint.NewLinter(p, lint.Profile(c.Profile))
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "RULE_COMPILE", err.Error(), "")
	}
	defer linter.Close()

	paths := c.Paths
	if len(paths) == 0 {
		paths = []string{"."}
	}
	w := &watch.Watcher{
		List:     func() ([]string, error) { return discover(paths) },
		Interval: time.Duration(c.Interval) * time.Millisecond,
	}

	fmt.Fprintln(cc.Stdout, cc.Faint(fmt.Sprintf("watching %s (profile %s) — Ctrl+C to stop",
		strings.Join(paths, ", "), c.Profile)))

	onChange := func(ev watch.Event) {
		for _, f := range ev.Removed {
			fmt.Fprintf(cc.Stdout, "%s %s\n", cc.Faint(cc.Glyphs().Dot), cc.Faint(f+" removed"))
		}
		for _, f := range ev.Changed {
			c.checkFile(ctx, cc, p, linter, f)
		}
	}

	err = w.Watch(ctx, true, onChange)
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(cc.Stdout, cc.Faint("stopped"))
		return nil
	}
	return err
}

// checkFile lints (and optionally fmt-checks) one file and prints the result.
func (c *watchCmd) checkFile(ctx context.Context, cc *clikit.Context, p *parse.Parser, linter *lint.Linter, f string) {
	src, err := os.ReadFile(f)
	if err != nil {
		return
	}
	findings, lerr := linter.Lint(ctx, src)
	if lerr != nil {
		fmt.Fprintln(cc.Stdout, cc.Failure(f+": "+lerr.Error()))
		return
	}
	unformatted := false
	if c.Fmt {
		if out, ferr := mfmt.Format(ctx, p, src, mfmt.Rules(mfmt.Canonical)); ferr == nil && string(out) != string(src) {
			unformatted = true
		}
	}
	if len(findings) == 0 && !unformatted {
		fmt.Fprintln(cc.Stdout, cc.Success(f))
		return
	}
	fmt.Fprintf(cc.Stdout, "%s %s\n", cc.Faint(cc.Glyphs().Arrow), cc.Accent(f))
	for _, fd := range findings {
		fmt.Fprintf(cc.Stdout, "  %s %d:%d  %s  %s\n",
			cc.Severity(string(fd.Severity)), fd.Line, fd.Col, cc.Faint(fd.Rule), fd.Message)
	}
	if unformatted {
		fmt.Fprintln(cc.Stdout, "  "+cc.Faint("needs formatting (m fmt --rules=canonical)"))
	}
}

// --- lsp ---------------------------------------------------------------------

type lspCmd struct{}

func (lspCmd) Run(_ *clikit.Context) error {
	srv, err := lsp.New(os.Stdin, os.Stdout)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "LSP_INIT", err.Error(), "")
	}
	defer srv.Close()
	if err := srv.Serve(); err != nil {
		return clikit.Fail(clikit.ExitRuntime, "LSP", err.Error(), "")
	}
	return nil
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
