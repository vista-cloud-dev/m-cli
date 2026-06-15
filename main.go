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

	"github.com/alecthomas/kong"
	"github.com/willabides/kongplete"

	"github.com/vista-cloud-dev/m-cli/clikit"
	"github.com/vista-cloud-dev/m-cli/internal/arch"
	"github.com/vista-cloud-dev/m-cli/internal/config"
	"github.com/vista-cloud-dev/m-cli/internal/dispatch"
	"github.com/vista-cloud-dev/m-cli/internal/engine"
	"github.com/vista-cloud-dev/m-cli/internal/harness"
	"github.com/vista-cloud-dev/m-cli/internal/lint"
	"github.com/vista-cloud-dev/m-cli/internal/lsp"
	"github.com/vista-cloud-dev/m-cli/internal/mcov"
	"github.com/vista-cloud-dev/m-cli/internal/mfmt"
	"github.com/vista-cloud-dev/m-cli/internal/mtest"
	"github.com/vista-cloud-dev/m-cli/internal/watch"
	"github.com/vista-cloud-dev/m-cli/internal/workspace"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// CLI is the root command grammar (one typed struct; spec §5).
type CLI struct {
	clikit.Globals

	Fmt      fmtCmd      `cmd:"" help:"Format M source over the parse tree (AST-preserving)."`
	Lint     lintCmd     `cmd:"" help:"Lint M source over the parse tree (query-driven rules)."`
	Lsp      lspCmd      `cmd:"" help:"Run the M language server (LSP 3.x over stdio)."`
	Test     testCmd     `cmd:"" help:"Run *TST.m suites through the engine (^STDASSERT)."`
	Coverage coverageCmd `cmd:"" help:"Line coverage over the engine (YDB view \"TRACE\" → LCOV)."`
	Watch    watchCmd    `cmd:"" help:"Re-run lint/fmt (and, with --run, tests) on M files as they change."`
	Vista    vistaCmd    `cmd:"" help:"Reach a live VistA via its m-<engine> driver (status / exec) — the driver-backed engine transport."`
	Arch     archCmd     `cmd:"" help:"Check the m/v waterline — engine-neutral vs VistA-specific layer boundary."`

	// Dispatched namespaces (spec §2.2): each forwards to a sibling binary.
	// irissync owns the IRIS source axis; kids-vc owns the KIDS round-trip.
	List   listCmd   `cmd:"" help:"List server routine docnames (→ irissync list)."`
	Pull   pullCmd   `cmd:"" help:"Materialize IRIS routine source → mirror (→ irissync pull)."`
	Status statusCmd `cmd:"" help:"Diff server vs. local manifest (→ irissync status)."`
	Verify verifyCmd `cmd:"" help:"Re-hash mirror vs. manifest (→ irissync verify)."`
	Push   pushCmd   `cmd:"" help:"Write edited routines back to IRIS — the sole DB writer (→ irissync push)."`
	Kids   kidsCmd   `cmd:"" help:"KIDS decompose/assemble/roundtrip/lint (→ kids-vc)."`

	Version versionCmd `cmd:"" help:"Show version, Go toolchain, and embedded grammar hash."`
	Schema  schemaCmd  `cmd:"" help:"Emit the aggregated command/flag/enum tree as JSON (agent discovery)."`

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
	Paths  []string `arg:"" optional:"" type:"path" help:"Files or directories to format (default: .)."`
	Rules  string   `default:"identity" enum:"identity,canonical" help:"Rule preset: identity (no-op) or canonical (overrides [fmt] rules)."`
	Config string   `type:"path" help:"Path to a .m-cli.toml / pyproject.toml (else discovered by walking up from CWD)."`
	Check  bool     `help:"Report files needing formatting; exit 3 if any (no writes)."`
	Write  bool     `short:"w" help:"Rewrite changed files in place."`
	Stdin  bool     `help:"Format stdin → stdout (raw; ignores paths and --output)."`
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

	cfg, err := loadProjectConfig(c.Config)
	if err != nil {
		return clikit.Fail(clikit.ExitUsage, "BAD_CONFIG", err.Error(), "")
	}
	// Layering: an explicitly-set --rules flag wins; otherwise [fmt] rules;
	// otherwise the identity preset. The flag default is "identity".
	rulesName := c.Rules
	if rulesName == "identity" && cfg.FmtRules != "" {
		rulesName = cfg.FmtRules
	}
	rules := mfmt.Rules(mfmt.Preset(rulesName))

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

	res := fmtResult{Rules: rulesName, Scanned: len(files)}
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

// loadProjectConfig resolves the project config: an explicit --config path is
// loaded directly; otherwise discovery walks up from the current directory. A
// malformed/invalid config is a hard usage error (faithful to the Python tool).
func loadProjectConfig(configFlag string) (config.Config, error) {
	if configFlag != "" {
		return config.LoadFile(configFlag)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return config.Config{}, nil
	}
	return config.LoadConfig(cwd)
}

// resolveLintFilter delegates to lint.ResolveFilter — the single rule-selection
// resolver shared by the CLI, watch, and the LSP so editor and CI never drift.
func resolveLintFilter(profileFlag string, cfg config.Config) string {
	return lint.ResolveFilter(profileFlag, cfg)
}

// --- lint --------------------------------------------------------------------

type lintCmd struct {
	Paths     []string `arg:"" optional:"" type:"path" help:"Files or directories to lint (default: .)."`
	Profile   string   `default:"default" enum:"default,modern,pythonic,pedantic,xindex,sac,vista,all" help:"Rule profile (overrides [lint] rules)."`
	Config    string   `type:"path" help:"Path to a .m-cli.toml / pyproject.toml (else discovered by walking up from CWD)."`
	Check     bool     `help:"Exit 3 if there are any findings (CI gate)."`
	ListRules bool     `help:"List the rules in the selected profile (then exit)."`
}

type ruleDoc struct {
	ID       string   `json:"id"`
	Severity string   `json:"severity"`
	Category string   `json:"category,omitempty"`
	Tags     []string `json:"tags"`
	Title    string   `json:"title"`
}

func (c *lintCmd) Run(cc *clikit.Context) error {
	cfg, err := loadProjectConfig(c.Config)
	if err != nil {
		return clikit.Fail(clikit.ExitUsage, "BAD_CONFIG", err.Error(), "")
	}
	opts := lint.OptionsFromConfig(cfg)
	filter := resolveLintFilter(c.Profile, cfg)
	rules, err := lint.Resolve(filter, opts)
	if err != nil {
		return clikit.Fail(clikit.ExitUsage, "BAD_PROFILE", err.Error(), "")
	}
	if len(rules) == 0 {
		return clikit.Fail(clikit.ExitUsage, "NO_RULES",
			fmt.Sprintf("no rules matched --profile/rules %q", filter), "")
	}

	if c.ListRules {
		docs := make([]ruleDoc, 0, len(rules))
		for _, r := range rules {
			docs = append(docs, ruleDoc{ID: r.ID, Severity: string(r.Severity), Category: r.Category, Tags: r.Tags, Title: r.Title})
		}
		return cc.Result(docs, func() {
			cc.Title("lint rules — " + filter)
			for _, d := range docs {
				fmt.Fprintf(cc.Stdout, "  %s  %s  %s\n", cc.Accent(d.ID), cc.Faint(d.Severity), d.Title)
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

	// Build the cross-routine workspace index when a selected rule needs it
	// (M-XINDX-007/008/049). A pre-pass over all discovered files indexes every
	// routine's labels + references so the rules can resolve LABEL^ROUTINE.
	needsWS := false
	for _, r := range rules {
		if r.NeedsWorkspace() {
			needsWS = true
			break
		}
	}
	if needsWS {
		ws := workspace.New()
		for _, f := range files {
			src, rerr := os.ReadFile(f)
			if rerr != nil {
				continue
			}
			tree, perr := p.Parse(ctx, src)
			if perr != nil {
				continue
			}
			ws.AddFile(strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)), tree.RootNode())
			tree.Close()
		}
		linter.AttachWorkspace(ws)
	}

	var diags []clikit.Diagnostic
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return clikit.Fail(clikit.ExitRuntime, "READ_FAILED", fmt.Sprintf("%s: %v", f, err), "")
		}
		routine := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		findings, err := linter.LintNamed(ctx, src, routine)
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
			fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("no findings (%d files, %s)", len(files), filter)))
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
	Resident  bool     `help:"Run ';; tier: integration' suites via the resident harness (RUN^STDHARN) and reconcile with file-side pure-logic suites (spec §9)."`
	Chset     string   `default:"" enum:",m,utf-8" help:"Engine charset: m (byte mode) or utf-8. Default: engine default (YDB inherits its ambient ydb_chset). Byte suites (STDCSPRNG/STDB64/STDHEX) need m on YDB; inherent on IRIS."`
}

type suiteResult struct {
	Suite  string `json:"suite"`
	Tier   string `json:"tier,omitempty"`
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
				eng = engine.New(kind, engine.Options{Runner: engine.DockerRunner(c.Docker, ""), Namespace: c.Namespace, Chset: c.Chset})
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
				eng = engine.New(kind, engine.Options{Runner: engine.DockerRunner(c.Docker, stageDir), Chset: c.Chset})
			}
		} else {
			eng = engine.New(kind, engine.Options{Namespace: c.Namespace, Chset: c.Chset})
		}
		var rows []suiteResult
		if c.Resident {
			rows, err = runTwoTier(ctx, eng, suites)
		} else {
			rows, err = runOneTier(ctx, eng, suites)
		}
		if err != nil {
			return err
		}
		for _, r := range rows {
			report.Passed += r.Passed
			report.Failed += r.Failed
			if !r.OK {
				failedSuites++
			}
			report.Results = append(report.Results, r)
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

// runOneTier is the default host-orchestrated path: every suite runs file-side.
func runOneTier(ctx context.Context, eng engine.Engine, suites []mtest.TestSuite) ([]suiteResult, error) {
	results, err := mtest.Run(ctx, eng, suites)
	if err != nil {
		return nil, clikit.Fail(clikit.ExitRuntime, "ENGINE_RUN", err.Error(),
			"m test runs on a live engine — is ydb/iris installed and reachable?")
	}
	tier := map[string]string{}
	for _, s := range suites {
		tier[s.Name] = s.Tier
	}
	rows := make([]suiteResult, 0, len(results))
	for _, r := range results {
		rows = append(rows, toRow(r, tier[r.Suite]))
	}
	return rows, nil
}

// runTwoTier (--resident) runs pure-logic suites file-side and integration
// suites via the resident harness, then reconciles by provenance (spec §9.1-Q6):
// one verdict per suite, exit = union.
func runTwoTier(ctx context.Context, eng engine.Engine, suites []mtest.TestSuite) ([]suiteResult, error) {
	var pureLogic, integration []mtest.TestSuite
	for _, s := range suites {
		if s.Tier == mtest.TierIntegration {
			integration = append(integration, s)
		} else {
			pureLogic = append(pureLogic, s)
		}
	}
	fileResults, err := mtest.Run(ctx, eng, pureLogic)
	if err != nil {
		return nil, clikit.Fail(clikit.ExitRuntime, "ENGINE_RUN", err.Error(),
			"m test runs on a live engine — is ydb/iris installed and reachable?")
	}
	resResults, err := harness.RunResident(ctx, eng, integration)
	if err != nil {
		return nil, clikit.Fail(clikit.ExitRuntime, "RESIDENT_RUN", err.Error(),
			"the resident harness needs RUN^STDHARN staged — add --routines <m-stdlib/src>")
	}
	merged := harness.Reconcile(fileResults, resResults)
	rows := make([]suiteResult, 0, len(merged.Results))
	for _, p := range merged.Results {
		rows = append(rows, toRow(p.Result, p.Tier))
	}
	return rows, nil
}

func toRow(r mtest.RunResult, tier string) suiteResult {
	return suiteResult{
		Suite: r.Suite, Tier: tier,
		Passed: r.Summary.Passed, Failed: r.Summary.Failed, Total: r.Summary.Total, OK: r.OK,
	}
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
	Chset      string   `default:"" enum:",m,utf-8" help:"Engine charset: m (byte mode) or utf-8. Default: engine default (YDB inherits its ambient ydb_chset). Byte suites (STDCSPRNG/STDB64/STDHEX) need m on YDB; inherent on IRIS."`
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
			eng = engine.New(kind, engine.Options{Runner: engine.DockerRunner(c.Docker, ""), Namespace: c.Namespace, Chset: c.Chset})
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
			eng = engine.New(kind, engine.Options{Runner: engine.DockerRunner(c.Docker, stageDir), Chset: c.Chset})
		}
	} else {
		eng = engine.New(kind, engine.Options{Namespace: c.Namespace, Chset: c.Chset})
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

	report := coverageReport{
		Engine: string(kind), Covered: result.Covered(), Total: result.Total(), Percent: result.Percent(),
	}
	for _, fc := range mcov.ByFile(result) {
		report.Files = append(report.Files, fileCov{Path: fc.Path, Covered: fc.Covered, Total: fc.Total})
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

func newStagedEngine(ctx context.Context, kind engine.Kind, docker, namespace, chset string, initialFiles []string) (*stagedEngine, error) {
	if docker == "" {
		return &stagedEngine{
			eng:     engine.New(kind, engine.Options{Namespace: namespace, Chset: chset}),
			restage: func([]string) error { return nil },
			cleanup: func() {},
		}, nil
	}
	if kind == engine.IRIS {
		stageDir := fmt.Sprintf("/tmp/m-eng-%d", time.Now().UnixNano())
		eng := engine.New(kind, engine.Options{Runner: engine.DockerRunner(docker, ""), Namespace: namespace, Chset: chset})
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
		eng:     engine.New(kind, engine.Options{Runner: engine.DockerRunner(docker, stageDir), Chset: chset}),
		restage: func(files []string) error { return engine.DockerStage(ctx, docker, stageDir, files) },
		cleanup: func() { engine.DockerUnstage(ctx, docker, stageDir) },
	}, nil
}

// --- watch -------------------------------------------------------------------

type watchCmd struct {
	Paths    []string `arg:"" optional:"" type:"path" help:"Files or directories to watch (default: .)."`
	Profile  string   `default:"default" enum:"default,modern,pythonic,pedantic,xindex,sac,vista,all" help:"Lint rule profile (overrides [lint] rules)."`
	Config   string   `type:"path" help:"Path to a .m-cli.toml / pyproject.toml (else discovered by walking up from CWD)."`
	Interval int      `default:"500" help:"Poll interval in milliseconds."`
	Fmt      bool     `help:"Also flag files that aren't canonically formatted."`

	// Run half (engine-bound): re-run *TST.m suites on each change.
	RunTests  bool     `name:"run" help:"Also run *TST.m suites on each change (the run half; needs an engine)."`
	Coverage  bool     `help:"Also report line coverage for changed routines on each change (implies --run; needs an engine)."`
	Engine    string   `help:"Engine for --run: ydb or iris (else $M_ENGINE / heuristic; exit 4 if unresolved)."`
	Docker    string   `help:"Run --run suites inside this container via docker exec."`
	Routines  []string `help:"Extra source dirs to stage for --run (e.g. m-stdlib/src). Repeatable."`
	Namespace string   `help:"IRIS namespace for --run (default USER)."`
	Chset     string   `default:"" enum:",m,utf-8" help:"Engine charset for --run: m (byte mode) or utf-8. Default: engine default. Byte suites need m on YDB; inherent on IRIS."`
}

func (c *watchCmd) Run(cc *clikit.Context) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	c.RunTests = c.RunTests || c.Coverage // coverage-on-save needs the engine-bound run path

	p, err := parse.New(ctx)
	if err != nil {
		return clikit.Fail(clikit.ExitRuntime, "PARSER_INIT", err.Error(), "")
	}
	defer func() { _ = p.Close(context.Background()) }()

	cfg, err := loadProjectConfig(c.Config)
	if err != nil {
		return clikit.Fail(clikit.ExitUsage, "BAD_CONFIG", err.Error(), "")
	}
	filter := resolveLintFilter(c.Profile, cfg)
	rules, err := lint.Resolve(filter, lint.OptionsFromConfig(cfg))
	if err != nil {
		return clikit.Fail(clikit.ExitUsage, "BAD_PROFILE", err.Error(), "")
	}
	linter, err := lint.NewLinter(p, rules)
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
		staged, err = newStagedEngine(ctx, kind, c.Docker, c.Namespace, c.Chset, files)
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
	if c.Coverage {
		mode = "lint+run+cov"
	}
	fmt.Fprintln(cc.Stdout, cc.Faint(fmt.Sprintf("watching %s (%s, %s) — Ctrl+C to stop",
		strings.Join(paths, ", "), mode, filter)))

	onChange := func(ev watch.Event) {
		for _, f := range ev.Removed {
			fmt.Fprintf(cc.Stdout, "%s %s\n", cc.Faint(cc.Glyphs().Dot), cc.Faint(f+" removed"))
		}
		for _, f := range ev.Changed {
			c.checkFile(ctx, cc, p, linter, f) // static half
		}
		if c.RunTests && len(ev.Changed) > 0 {
			c.runHalf(ctx, cc, p, staged, suites, ev.Changed) // run half
		}
	}

	err = w.Watch(ctx, true, onChange)
	if errors.Is(err, context.Canceled) {
		fmt.Fprintln(cc.Stdout, cc.Faint("stopped"))
		return nil
	}
	return err
}

// runHalf re-stages the changed files and re-runs the affected suites through
// the engine, printing a compact pass/fail summary (and, with --coverage, a
// per-routine coverage rollup) — the engine-bound half of m watch.
func (c *watchCmd) runHalf(ctx context.Context, cc *clikit.Context, p *parse.Parser, staged *stagedEngine, suites []mtest.TestSuite, changed []string) {
	if len(suites) == 0 {
		return
	}
	// Affected-test selection: run only the suites that exercise a changed
	// routine — the suite file itself changed, or it calls a changed routine —
	// rather than re-running the whole set on every save (spec §3.1/§9).
	changedRtns := map[string]bool{}
	for _, f := range changed {
		changedRtns[strings.ToUpper(strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)))] = true
	}
	affected := mtest.Affected(suites, changedRtns)
	if len(affected) == 0 {
		fmt.Fprintln(cc.Stdout, "  "+cc.Faint("tests: no suites affected"))
		return
	}
	if err := staged.restage(changed); err != nil {
		fmt.Fprintln(cc.Stdout, "  "+cc.Failure("run: stage failed: "+err.Error()))
		return
	}
	results, err := mtest.Run(ctx, staged.eng, affected)
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
	} else {
		fmt.Fprintln(cc.Stdout, "  "+cc.Failure(fmt.Sprintf("tests: %d/%d suites failed", fail, total)))
		for _, r := range results {
			if !r.OK {
				fmt.Fprintf(cc.Stdout, "    %s\n", cc.Failure(r.Suite))
			}
		}
	}
	if c.Coverage {
		c.coverageHalf(ctx, cc, p, staged, affected, changed) // coverage-on-save
	}
}

// coverageHalf measures line coverage for the changed routines while driving the
// affected suites through the engine, printing a per-routine rollup. Coverage is
// scoped to what just changed (not the whole tree) so the inner loop stays fast.
func (c *watchCmd) coverageHalf(ctx context.Context, cc *clikit.Context, p *parse.Parser, staged *stagedEngine, affected []mtest.TestSuite, changed []string) {
	var routinePaths []string
	for _, f := range changed {
		if isMFile(f) && !mtest.IsSuiteFile(f) {
			routinePaths = append(routinePaths, f)
		}
	}
	if len(routinePaths) == 0 {
		return // only suites changed — no routine-under-test to measure
	}
	suiteEntries := make([]string, len(affected))
	for i, s := range affected {
		suiteEntries[i] = s.Name
	}
	result, err := mcov.Run(ctx, p, staged.eng, routinePaths, suiteEntries)
	if err != nil {
		fmt.Fprintln(cc.Stdout, "  "+cc.Failure("cov: "+err.Error()))
		return
	}
	for _, fc := range mcov.ByFile(result) {
		fmt.Fprintf(cc.Stdout, "  %s %s  %d/%d  %s\n",
			cc.Faint("cov:"), filepath.Base(fc.Path), fc.Covered, fc.Total,
			cc.Faint(fmt.Sprintf("%.1f%%", fc.Percent())))
	}
}

// checkFile lints (and optionally fmt-checks) one file and prints the result.
func (c *watchCmd) checkFile(ctx context.Context, cc *clikit.Context, p *parse.Parser, linter *lint.Linter, f string) {
	src, err := os.ReadFile(f)
	if err != nil {
		return
	}
	routine := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
	findings, lerr := linter.LintNamed(ctx, src, routine)
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

// --- arch (the m/v waterline gates) ------------------------------------------
//
// The m/v waterline (docs/background/m-v-waterline-adr.md). The repo declares
// its layer in a committed meta artifact; `m arch check` runs, for an m-layer
// repo: G1 dependency-direction (Go closure carries no vista-cloud-dev/v-*
// module; M source references no VSL* routine) and G2 forbidden-symbol (M code
// references no VistA-only symbol — FileMan/Kernel/KIDS). A v-layer repo passes
// trivially (v → m, and VistA above the line, are allowed).

type archCmd struct {
	Check archCheckCmd `cmd:"" help:"Run the m/v waterline gates (G1 dependency-direction, G2 forbidden-symbol) for this repo."`
}

type archCheckCmd struct {
	Root  string `arg:"" optional:"" type:"path" help:"Repo root to check (default: .)."`
	Layer string `help:"Override the declared layer (m|v); else read from dist/repo.meta.json or dist/v-contract.json."`
}

func (c *archCheckCmd) Run(cc *clikit.Context) error {
	root := c.Root
	if root == "" {
		root = "."
	}
	rep, err := arch.Check(root, c.Layer)
	if err != nil {
		return clikit.Fail(clikit.ExitUsage, "ARCH_LAYER", err.Error(),
			`declare "layer": "m"|"v" in the repo meta, or pass --layer`)
	}

	if err := cc.Result(rep, func() {
		cc.Title("arch check")
		var checks []string
		if rep.CheckedGo {
			checks = append(checks, "go-deps")
		}
		if rep.CheckedM {
			checks = append(checks, "m-source")
		}
		if len(checks) == 0 {
			checks = append(checks, "none (v-layer)")
		}
		cc.KV(
			[2]string{"layer", cc.Accent(string(rep.Layer))},
			[2]string{"gates", "G1 dependency-direction, G2 forbidden-symbol"},
			[2]string{"checked", strings.Join(checks, ", ")},
			[2]string{"violations", fmt.Sprintf("%d", len(rep.Violations))},
		)
		for _, v := range rep.Violations {
			fmt.Fprintf(cc.Stdout, "  %s %s  %s  %s\n",
				cc.Severity("error"), cc.Accent(v.Gate), v.Source, cc.Faint(v.Detail))
		}
		if len(rep.Violations) == 0 {
			fmt.Fprintln(cc.Stdout, cc.Success("waterline clean"))
		}
	}); err != nil {
		return err
	}

	if len(rep.Violations) > 0 {
		return clikit.Fail(clikit.ExitCheck, "WATERLINE_VIOLATION",
			fmt.Sprintf("%d waterline violation(s)", len(rep.Violations)),
			"the m layer must not depend on the v layer (G1) or reference VistA symbols (G2)")
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

// --- dispatch (the busybox) --------------------------------------------------
//
// Each dispatched command captures its remaining args verbatim (Kong
// passthrough) and forwards them to a sibling binary via internal/dispatch.
// The native commands above are `m`'s own; everything here is a sibling's
// surface fronted under one `m` (spec §2.2).

// pass is the shared passthrough arg: every token after the verb is forwarded
// to the sibling untouched (flags included).
type pass struct {
	Rest []string `arg:"" optional:"" passthrough:"" help:"Arguments forwarded verbatim to the sibling binary."`
}

type (
	listCmd   struct{ pass }
	pullCmd   struct{ pass }
	statusCmd struct{ pass }
	verifyCmd struct{ pass }
	pushCmd   struct{ pass }
	kidsCmd   struct{ pass }
)

func (c *listCmd) Run(cc *clikit.Context) error   { return dispatchRun(cc, "list", c.Rest) }
func (c *pullCmd) Run(cc *clikit.Context) error   { return dispatchRun(cc, "pull", c.Rest) }
func (c *statusCmd) Run(cc *clikit.Context) error { return dispatchRun(cc, "status", c.Rest) }
func (c *verifyCmd) Run(cc *clikit.Context) error { return dispatchRun(cc, "verify", c.Rest) }
func (c *pushCmd) Run(cc *clikit.Context) error   { return dispatchRun(cc, "push", c.Rest) }
func (c *kidsCmd) Run(cc *clikit.Context) error   { return dispatchRun(cc, "kids", c.Rest) }

// dispatchRun forwards a dispatched verb to its sibling, faithfully relaying the
// child's exit code. A resolution failure surfaces as `m`'s own deterministic
// error (rendered by clikit); a non-zero child exit becomes `m`'s exit code
// without double-rendering, since the child already wrote its own output.
func dispatchRun(cc *clikit.Context, verb string, rest []string) error {
	spec, ok := dispatch.Find(verb)
	if !ok {
		return clikit.Fail(clikit.ExitUsage, "UNKNOWN_DISPATCH", "no sibling dispatch for "+verb, "")
	}
	code, err := dispatch.Run(context.Background(), spec, dispatchGlobals(cc), rest,
		os.Stdin, os.Stdout, os.Stderr)
	if err != nil {
		return err
	}
	if code != clikit.ExitOK {
		os.Exit(code)
	}
	return nil
}

// dispatchGlobals reconstructs the toolchain-wide global flags `m` resolved so
// the sibling renders/behaves identically — Kong consumes these even after the
// verb, so they must be re-forwarded (the siblings share clikit.Globals).
func dispatchGlobals(cc *clikit.Context) []string {
	g := []string{"--output", string(cc.Format)}
	if cc.Verbose {
		g = append(g, "--verbose")
	}
	if cc.Format == clikit.FormatText && !cc.Color {
		g = append(g, "--no-color")
	}
	return g
}

// --- schema (aggregated) -----------------------------------------------------

// schemaCmd emits the full command tree as JSON like clikit's, then merges each
// available sibling's sub-schema so an agent sees one tree (spec §2.2/§5.5).
type schemaCmd struct{}

func (schemaCmd) Run(cc *clikit.Context, k *kong.Kong) error {
	doc := clikit.BuildSchema(k, k.Model.Name, clikit.Version)
	doc = dispatch.Aggregate(context.Background(), doc)
	return cc.EmitJSON(doc)
}
