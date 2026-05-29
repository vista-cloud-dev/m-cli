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
	"github.com/vista-cloud-dev/m-cli/internal/mcov"
	"github.com/vista-cloud-dev/m-cli/internal/mfmt"
	"github.com/vista-cloud-dev/m-cli/internal/mtest"
	"github.com/vista-cloud-dev/m-cli/internal/watch"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// CLI is the root command grammar (one typed struct; spec §5).
type CLI struct {
	clikit.Globals

	Fmt      fmtCmd           `cmd:"" help:"Format M source over the parse tree (AST-preserving)."`
	Lint     lintCmd          `cmd:"" help:"Lint M source over the parse tree (query-driven rules)."`
	Lsp      lspCmd           `cmd:"" help:"Run the M language server (LSP 3.x over stdio)."`
	Test     testCmd          `cmd:"" help:"Run *TST.m suites through the engine (^STDASSERT)."`
	Coverage coverageCmd      `cmd:"" help:"Line coverage over the engine (YDB view \"TRACE\" → LCOV)."`
	Watch    watchCmd         `cmd:"" help:"Re-run lint/fmt (and, with --run, tests) on M files as they change."`
	Version  versionCmd       `cmd:"" help:"Show version, Go toolchain, and embedded grammar hash."`
	Schema   clikit.SchemaCmd `cmd:"" help:"Emit the command/flag/enum tree as JSON (agent discovery)."`

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
	Paths     []string `arg:"" optional:"" type:"path" help:"Suites or directories to run (default: .)."`
	Engine    string   `help:"Engine: ydb or iris. Else $M_ENGINE / heuristic; refuses (exit 4) if unresolved."`
	Docker    string   `help:"Run inside this running container via docker exec (e.g. m-test-engine, vista-iris)."`
	Routines  []string `help:"Extra source dirs to stage (e.g. m-stdlib/src for ^STDASSERT). Repeatable."`
	Namespace string   `help:"IRIS namespace (default USER)."`
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
			// Stage the suites (+ any --routines deps like ^STDASSERT) into the
			// container and run there via docker exec. The mechanism is
			// engine-specific: YDB drops raw .m on $ydb_routines (auto-compile);
			// IRIS UDL-wraps + OBJ.Loads every routine (no compile-from-path).
			var files []string
			for _, s := range suites {
				files = append(files, s.Path)
			}
			for _, rdir := range c.Routines {
				ms, _ := filepath.Glob(filepath.Join(rdir, "*.m"))
				files = append(files, ms...)
			}
			if kind == engine.IRIS {
				stageDir := fmt.Sprintf("/tmp/m-test-%d", time.Now().UnixNano())
				eng = engine.New(kind, engine.Options{Runner: engine.DockerRunner(c.Docker, ""), Namespace: c.Namespace})
				if err := engine.IrisStageLoad(ctx, eng, c.Docker, stageDir, files); err != nil {
					return clikit.Fail(clikit.ExitRuntime, "STAGE_FAILED", err.Error(), "")
				}
				defer engine.DockerUnstage(ctx, c.Docker, stageDir)
			} else {
				stageDir := fmt.Sprintf("/m-work/m-test-%d", time.Now().UnixNano())
				if err := engine.DockerStage(ctx, c.Docker, stageDir, files); err != nil {
					return clikit.Fail(clikit.ExitRuntime, "STAGE_FAILED", err.Error(), "")
				}
				defer engine.DockerUnstage(ctx, c.Docker, stageDir)
				eng = engine.New(kind, engine.Options{Runner: engine.DockerRunner(c.Docker, stageDir)})
			}
		} else {
			eng = engine.New(kind, engine.Options{Namespace: c.Namespace})
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

// --- coverage ----------------------------------------------------------------

type coverageCmd struct {
	Paths      []string `arg:"" optional:"" type:"path" help:"Routines + suites, or directories (default: .)."`
	Engine     string   `help:"Engine: ydb or iris. Else $M_ENGINE / heuristic; refuses (exit 4) if unresolved."`
	Docker     string   `help:"Run inside this running container via docker exec (e.g. m-test-engine)."`
	Routines   []string `help:"Extra source dirs to stage (e.g. m-stdlib/src). Repeatable."`
	Namespace  string   `help:"IRIS namespace (default USER)."`
	MinPercent float64  `name:"min-percent" help:"Fail (exit 3) if line coverage is below this percent."`
	Lcov       string   `help:"Write an LCOV tracefile to this path."`
}

type fileCov struct {
	Path    string `json:"path"`
	Covered int    `json:"covered"`
	Total   int    `json:"total"`
}

type coverageReport struct {
	Engine  string    `json:"engine"`
	Covered int       `json:"coveredLines"`
	Total   int       `json:"totalLines"`
	Percent float64   `json:"linePercent"`
	Files   []fileCov `json:"files"`
}

func (c *coverageCmd) Run(cc *clikit.Context) error {
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
	allFiles, err := discover(paths)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "DISCOVER_FAILED", err.Error(), "")
	}
	var routinePaths, suiteEntries []string
	for _, f := range allFiles {
		if mtest.IsSuiteFile(f) {
			suiteEntries = append(suiteEntries, strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)))
		} else {
			routinePaths = append(routinePaths, f)
		}
	}

	var eng engine.Engine
	if c.Docker != "" {
		files := append([]string{}, allFiles...)
		for _, rdir := range c.Routines {
			ms, _ := filepath.Glob(filepath.Join(rdir, "*.m"))
			files = append(files, ms...)
		}
		if kind == engine.IRIS {
			stageDir := fmt.Sprintf("/tmp/m-cov-%d", time.Now().UnixNano())
			eng = engine.New(kind, engine.Options{Runner: engine.DockerRunner(c.Docker, ""), Namespace: c.Namespace})
			if err := engine.IrisStageLoad(ctx, eng, c.Docker, stageDir, files); err != nil {
				return clikit.Fail(clikit.ExitRuntime, "STAGE_FAILED", err.Error(), "")
			}
			defer engine.DockerUnstage(ctx, c.Docker, stageDir)
		} else {
			stageDir := fmt.Sprintf("/m-work/m-cov-%d", time.Now().UnixNano())
			if err := engine.DockerStage(ctx, c.Docker, stageDir, files); err != nil {
				return clikit.Fail(clikit.ExitRuntime, "STAGE_FAILED", err.Error(), "")
			}
			defer engine.DockerUnstage(ctx, c.Docker, stageDir)
			eng = engine.New(kind, engine.Options{Runner: engine.DockerRunner(c.Docker, stageDir)})
		}
	} else {
		eng = engine.New(kind, engine.Options{Namespace: c.Namespace})
	}

	result, err := mcov.Run(ctx, p, eng, routinePaths, suiteEntries)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "COVERAGE_RUN", err.Error(),
			"m coverage runs on a live engine — is ydb/iris installed and reachable?")
	}

	if c.Lcov != "" {
		if err := os.WriteFile(c.Lcov, []byte(mcov.LCOV(result)), 0o644); err != nil {
			return clikit.Fail(clikit.ExitRuntime, "LCOV_WRITE", err.Error(), "")
		}
	}

	// Per-file rollup.
	type acc struct{ cov, tot int }
	byPath := map[string]*acc{}
	var order []string
	for _, l := range result.Lines {
		a := byPath[l.Path]
		if a == nil {
			a = &acc{}
			byPath[l.Path] = a
			order = append(order, l.Path)
		}
		a.tot++
		if l.Hits > 0 {
			a.cov++
		}
	}
	report := coverageReport{
		Engine: string(kind), Covered: result.Covered(), Total: result.Total(), Percent: result.Percent(),
	}
	for _, path := range order {
		report.Files = append(report.Files, fileCov{Path: path, Covered: byPath[path].cov, Total: byPath[path].tot})
	}

	if err := cc.Result(report, func() {
		cc.Title("coverage")
		cc.KV(
			[2]string{"engine", cc.Accent(report.Engine)},
			[2]string{"lines", fmt.Sprintf("%d/%d covered", report.Covered, report.Total)},
			[2]string{"percent", fmt.Sprintf("%.1f%%", report.Percent)},
		)
		for _, f := range report.Files {
			pct := 0.0
			if f.Total > 0 {
				pct = 100 * float64(f.Covered) / float64(f.Total)
			}
			fmt.Fprintf(cc.Stdout, "  %s  %d/%d  %s\n", f.Path, f.Covered, f.Total, cc.Faint(fmt.Sprintf("%.1f%%", pct)))
		}
		if c.Lcov != "" {
			fmt.Fprintln(cc.Stdout, cc.Faint("wrote LCOV → "+c.Lcov))
		}
	}); err != nil {
		return err
	}

	if c.MinPercent > 0 && report.Total > 0 && report.Percent < c.MinPercent {
		return clikit.Fail(clikit.ExitCheck, "BELOW_MIN_COVERAGE",
			fmt.Sprintf("line coverage %.1f%% is below the %.1f%% minimum", report.Percent, c.MinPercent), "")
	}
	return nil
}

// --- engine staging (shared by the engine-bound run paths) ------------------

// stagedEngine is an engine plus, for the docker transport, a re-stage hook and
// cleanup. It unifies engine-bound staging: YDB drops raw .m on $ydb_routines
// (auto-compile); IRIS UDL-wraps + OBJ.Loads (no compile-from-path). restage
// pushes a changed subset into the same stage dir — used by `m watch --run`.
type stagedEngine struct {
	eng     engine.Engine
	restage func(files []string) error
	cleanup func()
}

func newStagedEngine(ctx context.Context, kind engine.Kind, docker, namespace string, initialFiles []string) (*stagedEngine, error) {
	if docker == "" {
		return &stagedEngine{
			eng:     engine.New(kind, engine.Options{Namespace: namespace}),
			restage: func([]string) error { return nil },
			cleanup: func() {},
		}, nil
	}
	if kind == engine.IRIS {
		stageDir := fmt.Sprintf("/tmp/m-eng-%d", time.Now().UnixNano())
		eng := engine.New(kind, engine.Options{Runner: engine.DockerRunner(docker, ""), Namespace: namespace})
		restage := func(files []string) error { return engine.IrisStageLoad(ctx, eng, docker, stageDir, files) }
		if err := restage(initialFiles); err != nil {
			return nil, err
		}
		return &stagedEngine{eng: eng, restage: restage, cleanup: func() { engine.DockerUnstage(ctx, docker, stageDir) }}, nil
	}
	stageDir := fmt.Sprintf("/m-work/m-eng-%d", time.Now().UnixNano())
	if err := engine.DockerStage(ctx, docker, stageDir, initialFiles); err != nil {
		return nil, err
	}
	return &stagedEngine{
		eng:     engine.New(kind, engine.Options{Runner: engine.DockerRunner(docker, stageDir)}),
		restage: func(files []string) error { return engine.DockerStage(ctx, docker, stageDir, files) },
		cleanup: func() { engine.DockerUnstage(ctx, docker, stageDir) },
	}, nil
}

// --- watch -------------------------------------------------------------------

type watchCmd struct {
	Paths    []string `arg:"" optional:"" type:"path" help:"Files or directories to watch (default: .)."`
	Profile  string   `default:"default" enum:"default,modern,all" help:"Lint rule profile."`
	Interval int      `default:"500" help:"Poll interval in milliseconds."`
	Fmt      bool     `help:"Also flag files that aren't canonically formatted."`

	// Run half (engine-bound): re-run *TST.m suites on each change.
	RunTests  bool     `name:"run" help:"Also run *TST.m suites on each change (the run half; needs an engine)."`
	Engine    string   `help:"Engine for --run: ydb or iris (else $M_ENGINE / heuristic; exit 4 if unresolved)."`
	Docker    string   `help:"Run --run suites inside this container via docker exec."`
	Routines  []string `help:"Extra source dirs to stage for --run (e.g. m-stdlib/src). Repeatable."`
	Namespace string   `help:"IRIS namespace for --run (default USER)."`
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

	// Run-half setup (the engine-bound half of the bisection). Built once at
	// startup; on each change we re-stage the changed files and re-run suites.
	var staged *stagedEngine
	var suites []mtest.TestSuite
	if c.RunTests {
		kind, explicit, err := engine.Resolve(engine.DetectConfig(c.Engine))
		if err != nil {
			return clikit.Fail(clikit.ExitUsage, "BAD_ENGINE", err.Error(), "use --engine ydb|iris")
		}
		if !explicit {
			return clikit.Fail(clikit.ExitRefused, "ENGINE_UNRESOLVED",
				"--run is engine-bound but no engine resolved",
				"pass --engine ydb|iris, set $M_ENGINE, or run where IRIS is detected")
		}
		suites, err = mtest.Discover(p, paths)
		if err != nil {
			return clikit.Fail(clikit.ExitRuntime, "DISCOVER_FAILED", err.Error(), "")
		}
		files, _ := discover(paths)
		for _, rdir := range c.Routines {
			ms, _ := filepath.Glob(filepath.Join(rdir, "*.m"))
			files = append(files, ms...)
		}
		staged, err = newStagedEngine(ctx, kind, c.Docker, c.Namespace, files)
		if err != nil {
			return clikit.Fail(clikit.ExitRuntime, "STAGE_FAILED", err.Error(), "")
		}
		defer staged.cleanup()
	}

	w := &watch.Watcher{
		List:     func() ([]string, error) { return discover(paths) },
		Interval: time.Duration(c.Interval) * time.Millisecond,
	}

	mode := "lint"
	if c.RunTests {
		mode = "lint+run"
	}
	fmt.Fprintln(cc.Stdout, cc.Faint(fmt.Sprintf("watching %s (%s, profile %s) — Ctrl+C to stop",
		strings.Join(paths, ", "), mode, c.Profile)))

	onChange := func(ev watch.Event) {
		for _, f := range ev.Removed {
			fmt.Fprintf(cc.Stdout, "%s %s\n", cc.Faint(cc.Glyphs().Dot), cc.Faint(f+" removed"))
		}
		for _, f := range ev.Changed {
			c.checkFile(ctx, cc, p, linter, f) // static half
		}
		if c.RunTests && len(ev.Changed) > 0 {
			c.runHalf(ctx, cc, staged, suites, ev.Changed) // run half
		}
	}

	err = w.Watch(ctx, true, onChange)
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(cc.Stdout, cc.Faint("stopped"))
		return nil
	}
	return err
}

// runHalf re-stages the changed files and re-runs the suites through the engine,
// printing a compact pass/fail summary (the engine-bound half of m watch).
func (c *watchCmd) runHalf(ctx context.Context, cc *clikit.Context, staged *stagedEngine, suites []mtest.TestSuite, changed []string) {
	if len(suites) == 0 {
		return
	}
	if err := staged.restage(changed); err != nil {
		fmt.Fprintln(cc.Stdout, "  "+cc.Failure("run: stage failed: "+err.Error()))
		return
	}
	results, err := mtest.Run(ctx, staged.eng, suites)
	if err != nil {
		fmt.Fprintln(cc.Stdout, "  "+cc.Failure("run: "+err.Error()))
		return
	}
	var pass, fail int
	for _, r := range results {
		if r.OK {
			pass++
		} else {
			fail++
		}
	}
	total := pass + fail
	if fail == 0 {
		fmt.Fprintln(cc.Stdout, "  "+cc.Success(fmt.Sprintf("tests: %d/%d suites ok", pass, total)))
		return
	}
	fmt.Fprintln(cc.Stdout, "  "+cc.Failure(fmt.Sprintf("tests: %d/%d suites failed", fail, total)))
	for _, r := range results {
		if !r.OK {
			fmt.Fprintf(cc.Stdout, "    %s\n", cc.Failure(r.Suite))
		}
	}
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
