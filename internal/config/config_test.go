package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/config"
)

// write writes content to dir/name and returns the full path.
func write(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFindConfigWalkUp(t *testing.T) {
	root := t.TempDir()
	cfg := write(t, root, ".m-cli.toml", "[lint]\nrules = \"all\"\n")
	deep := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	got := config.FindConfig(deep)
	if got != cfg {
		t.Fatalf("FindConfig(%q) = %q, want %q", deep, got, cfg)
	}
}

func TestFindConfigStartIsFile(t *testing.T) {
	root := t.TempDir()
	cfg := write(t, root, ".m-cli.toml", "[lint]\nrules = \"all\"\n")
	src := write(t, filepath.Join(root, "sub"), "ROUTINE.m", "X ;\n")
	if got := config.FindConfig(src); got != cfg {
		t.Fatalf("FindConfig(file) = %q, want %q", got, cfg)
	}
}

func TestFindConfigGitBoundaryStops(t *testing.T) {
	root := t.TempDir()
	// Config lives ABOVE the .git boundary; the walk must not escape to it.
	write(t, root, ".m-cli.toml", "[lint]\nrules = \"all\"\n")
	proj := filepath.Join(root, "proj")
	write(t, proj, ".git", "") // a .git file is enough to mark the boundary
	deep := filepath.Join(proj, "x", "y")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := config.FindConfig(deep); got != "" {
		t.Fatalf("FindConfig past .git = %q, want \"\" (boundary should stop the walk)", got)
	}
}

func TestFindConfigAtGitBoundaryIsFound(t *testing.T) {
	root := t.TempDir()
	proj := filepath.Join(root, "proj")
	cfg := write(t, proj, ".m-cli.toml", "[lint]\nrules = \"all\"\n")
	write(t, proj, ".git", "")
	deep := filepath.Join(proj, "x")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	// The boundary check runs AFTER the per-level config check, so a config
	// sitting at the .git directory is still found.
	if got := config.FindConfig(deep); got != cfg {
		t.Fatalf("FindConfig at .git dir = %q, want %q", got, cfg)
	}
}

func TestFindConfigPrefersMCLIOverPyproject(t *testing.T) {
	root := t.TempDir()
	cfg := write(t, root, ".m-cli.toml", "[lint]\nrules = \"all\"\n")
	write(t, root, "pyproject.toml", "[tool.m-cli]\n[tool.m-cli.lint]\nrules = \"modern\"\n")
	if got := config.FindConfig(root); got != cfg {
		t.Fatalf("FindConfig = %q, want .m-cli.toml %q", got, cfg)
	}
}

func TestFindConfigPyprojectWithTable(t *testing.T) {
	root := t.TempDir()
	py := write(t, root, "pyproject.toml", "[tool.m-cli]\n[tool.m-cli.lint]\nrules = \"modern\"\n")
	if got := config.FindConfig(root); got != py {
		t.Fatalf("FindConfig = %q, want pyproject %q", got, py)
	}
}

func TestFindConfigPyprojectWithoutTableIgnored(t *testing.T) {
	root := t.TempDir()
	// A pyproject with no [tool.m-cli] table is not an m-cli config; the walk
	// continues past it.
	write(t, root, "pyproject.toml", "[tool.black]\nline-length = 88\n")
	if got := config.FindConfig(root); got != "" {
		t.Fatalf("FindConfig = %q, want \"\" (pyproject without [tool.m-cli])", got)
	}
}

func TestLoadConfigAbsentIsEmpty(t *testing.T) {
	root := t.TempDir()
	cfg, err := config.LoadConfig(root)
	if err != nil {
		t.Fatalf("LoadConfig(empty dir): %v", err)
	}
	if cfg.LintRules != "" || len(cfg.LintDisable) != 0 || cfg.SourcePath != "" {
		t.Fatalf("absent config not empty: %+v", cfg)
	}
}

func TestLoadConfigPyprojectTable(t *testing.T) {
	root := t.TempDir()
	write(t, root, "pyproject.toml",
		"[tool.m-cli.lint]\nrules = \"modern\"\ndisable = [\"M-MOD-009\"]\n")
	cfg, err := config.LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LintRules != "modern" {
		t.Errorf("LintRules = %q, want modern", cfg.LintRules)
	}
	if len(cfg.LintDisable) != 1 || cfg.LintDisable[0] != "M-MOD-009" {
		t.Errorf("LintDisable = %v", cfg.LintDisable)
	}
}

func TestLoadConfigSections(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".m-cli.toml", `
[lint]
rules = "modern"
disable = ["M-MOD-009", "M-STY-001"]
target_engine = "YottaDB"

[lint.severity]
"M-MOD-001" = "Warning"

[lint.thresholds]
line_length = 100
commands_per_line = 1

[lint.taint]
formals_tainted = false
extra_sanitizers = ["$tr", "$justify"]

[lint.vista]
kernel_locals = "default"
trusted_routines = ["XLFSTR", "DILF"]

[fmt]
rules = "canonical"
`)
	cfg, err := config.LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LintRules != "modern" {
		t.Errorf("LintRules = %q", cfg.LintRules)
	}
	if cfg.LintTargetEngine != "yottadb" {
		t.Errorf("target_engine = %q, want normalized yottadb", cfg.LintTargetEngine)
	}
	if cfg.LintSeverityOverrides["M-MOD-001"] != "warning" {
		t.Errorf("severity override = %q, want warning", cfg.LintSeverityOverrides["M-MOD-001"])
	}
	if cfg.LintThresholds["line_length"] != 100 || cfg.LintThresholds["commands_per_line"] != 1 {
		t.Errorf("thresholds = %v", cfg.LintThresholds)
	}
	if cfg.LintTaintFormalsTainted == nil || *cfg.LintTaintFormalsTainted != false {
		t.Errorf("formals_tainted = %v, want false", cfg.LintTaintFormalsTainted)
	}
	if len(cfg.LintTaintExtraSanitizers) != 2 || cfg.LintTaintExtraSanitizers[0] != "$TR" {
		t.Errorf("extra_sanitizers = %v, want upper-cased", cfg.LintTaintExtraSanitizers)
	}
	if len(cfg.LintVistaKernelLocals) != 1 || cfg.LintVistaKernelLocals[0] != "default" {
		t.Errorf("kernel_locals = %v, want [default]", cfg.LintVistaKernelLocals)
	}
	if len(cfg.LintVistaTrustedRoutines) != 2 {
		t.Errorf("trusted_routines = %v", cfg.LintVistaTrustedRoutines)
	}
	if cfg.FmtRules != "canonical" {
		t.Errorf("fmt rules = %q", cfg.FmtRules)
	}
	if cfg.SourcePath == "" {
		t.Error("SourcePath should be set")
	}
}

func TestLoadConfigUnknownKeysIgnored(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".m-cli.toml", "[lint]\nrules = \"all\"\nbogus_key = 42\n\n[totally_unknown]\nx = 1\n")
	cfg, err := config.LoadConfig(root)
	if err != nil {
		t.Fatalf("unknown keys should be ignored, got %v", err)
	}
	if cfg.LintRules != "all" {
		t.Errorf("LintRules = %q", cfg.LintRules)
	}
}

func TestLoadConfigKernelLocalsList(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".m-cli.toml", "[lint.vista]\nkernel_locals = [\"U\", \"DT\"]\n")
	cfg, err := config.LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.LintVistaKernelLocals) != 2 || cfg.LintVistaKernelLocals[0] != "U" {
		t.Errorf("kernel_locals = %v, want [U DT]", cfg.LintVistaKernelLocals)
	}
}

func TestLoadConfigMalformed(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string // substring expected in the error
	}{
		{"bad TOML", "[lint\nrules = ", "invalid TOML"},
		{"disable not a list", "[lint]\ndisable = \"M-MOD-009\"\n", "disable must be a list"},
		{"bad severity name", "[lint.severity]\n\"M-MOD-001\" = \"loud\"\n", "unknown severity"},
		{"severity not string", "[lint.severity]\n\"M-MOD-001\" = 3\n", "expected a string"},
		{"non-int threshold", "[lint.thresholds]\nline_length = 1.5\n", "positive integer"},
		{"non-positive threshold", "[lint.thresholds]\nline_length = 0\n", "positive integer"},
		{"unknown threshold key", "[lint.thresholds]\nwidth = 80\n", "unknown threshold"},
		{"bad target_engine", "[lint]\ntarget_engine = \"mumps\"\n", "unknown"},
		{"target_engine not string", "[lint]\ntarget_engine = 3\n", "must be a string"},
		{"formals_tainted non-bool", "[lint.taint]\nformals_tainted = \"yes\"\n", "must be a boolean"},
		{"kernel_locals bad string", "[lint.vista]\nkernel_locals = \"all\"\n", "must be \"default\""},
		{"trusted_routines bad string", "[lint.vista]\ntrusted_routines = \"all\"\n", "must be \"default\""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := write(t, root, ".m-cli.toml", tc.toml)
			_, err := config.LoadConfig(root)
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.want)
			}
			if !contains(err.Error(), tc.want) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.want)
			}
			// A malformed config is a hard error that names the source path.
			if !contains(err.Error(), path) {
				t.Errorf("error %q should name the source path %q", err.Error(), path)
			}
		})
	}
}

func TestKnownThresholdsAndEngines(t *testing.T) {
	for _, k := range []string{
		"line_length", "code_line_length", "routine_lines", "label_lines",
		"cyclomatic", "cognitive", "dot_block_depth", "argument_count",
		"commands_per_line", "comment_density_pct",
	} {
		if _, ok := config.KnownThresholds[k]; !ok {
			t.Errorf("KnownThresholds missing %q", k)
		}
	}
	if config.KnownThresholds["line_length"] != 200 {
		t.Errorf("line_length default = %d, want 200", config.KnownThresholds["line_length"])
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
