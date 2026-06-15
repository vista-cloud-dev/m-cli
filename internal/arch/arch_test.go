package arch

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// --- ResolveLayer ------------------------------------------------------------

func TestResolveLayerOverrideWins(t *testing.T) {
	dir := t.TempDir()
	// A repo.meta.json declaring "m" must be overridden by an explicit "v".
	writeFile(t, filepath.Join(dir, "dist", "repo.meta.json"), `{"layer":"m"}`)
	got, err := ResolveLayer(dir, "v")
	if err != nil {
		t.Fatalf("ResolveLayer: %v", err)
	}
	if got != LayerV {
		t.Errorf("override: got %q, want v", got)
	}
}

func TestResolveLayerFromRepoMeta(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dist", "repo.meta.json"), `{"id":"tool:x","layer":"m"}`)
	got, err := ResolveLayer(dir, "")
	if err != nil {
		t.Fatalf("ResolveLayer: %v", err)
	}
	if got != LayerM {
		t.Errorf("repo.meta: got %q, want m", got)
	}
}

func TestResolveLayerFromVContract(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dist", "v-contract.json"), `{"domain":"pkg","layer":"v"}`)
	got, err := ResolveLayer(dir, "")
	if err != nil {
		t.Fatalf("ResolveLayer: %v", err)
	}
	if got != LayerV {
		t.Errorf("v-contract: got %q, want v", got)
	}
}

func TestResolveLayerFromRootMeta(t *testing.T) {
	// A repo whose dist/ is gitignored (e.g. m-cli) declares layer in a
	// root-level repo.meta.json instead.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "repo.meta.json"), `{"id":"tool:m-cli","layer":"m"}`)
	got, err := ResolveLayer(dir, "")
	if err != nil {
		t.Fatalf("ResolveLayer: %v", err)
	}
	if got != LayerM {
		t.Errorf("root repo.meta: got %q, want m", got)
	}
}

func TestResolveLayerMissingIsError(t *testing.T) {
	dir := t.TempDir()
	if _, err := ResolveLayer(dir, ""); err == nil {
		t.Error("expected an error when no layer is declared, got nil")
	}
}

func TestResolveLayerBadOverride(t *testing.T) {
	dir := t.TempDir()
	if _, err := ResolveLayer(dir, "x"); err == nil {
		t.Error("expected an error for an invalid override, got nil")
	}
}

// --- parseGoListDeps ---------------------------------------------------------

func TestParseGoListDeps(t *testing.T) {
	// `go list -deps -json` emits a stream of concatenated package objects;
	// some packages (stdlib) carry no Module.
	stream := []byte(`{"ImportPath":"fmt"}
{"ImportPath":"github.com/vista-cloud-dev/m-cli/clikit","Module":{"Path":"github.com/vista-cloud-dev/m-cli"}}
{"ImportPath":"github.com/vista-cloud-dev/v-pkg/pkgcli","Module":{"Path":"github.com/vista-cloud-dev/v-pkg"}}
{"ImportPath":"github.com/vista-cloud-dev/m-cli/internal/arch","Module":{"Path":"github.com/vista-cloud-dev/m-cli"}}`)
	mods, err := parseGoListDeps(stream)
	if err != nil {
		t.Fatalf("parseGoListDeps: %v", err)
	}
	// Distinct module paths only (the two m-cli packages collapse to one).
	if !contains(mods, "github.com/vista-cloud-dev/m-cli") ||
		!contains(mods, "github.com/vista-cloud-dev/v-pkg") {
		t.Errorf("expected both module paths, got %v", mods)
	}
	if n := count(mods, "github.com/vista-cloud-dev/m-cli"); n != 1 {
		t.Errorf("expected distinct modules, m-cli appeared %d times", n)
	}
}

// --- vViolations -------------------------------------------------------------

func TestVViolationsFlagsVModules(t *testing.T) {
	mods := []string{
		"github.com/vista-cloud-dev/m-cli",
		"github.com/vista-cloud-dev/m-driver-sdk",
		"github.com/vista-cloud-dev/v-pkg",
		"github.com/alecthomas/kong",
	}
	vs := vViolations(mods)
	if len(vs) != 1 {
		t.Fatalf("expected 1 violation, got %d: %v", len(vs), vs)
	}
	if vs[0].Gate != "G1" || vs[0].Kind != "go-dep" || vs[0].Source != "github.com/vista-cloud-dev/v-pkg" {
		t.Errorf("unexpected violation: %+v", vs[0])
	}
}

func TestVViolationsCleanClosure(t *testing.T) {
	mods := []string{
		"github.com/vista-cloud-dev/m-cli",
		"github.com/vista-cloud-dev/m-driver-sdk",
		"github.com/alecthomas/kong",
	}
	if vs := vViolations(mods); len(vs) != 0 {
		t.Errorf("expected no violations for a clean m closure, got %v", vs)
	}
}

// --- CheckMRefs --------------------------------------------------------------

func TestCheckMRefsFlagsVSLCall(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "STDX.m"),
		"STDX ;\n clean() ;\n  set x=$$cfg^VSLCFG(\"a\")\n  quit\n")
	vs, err := CheckMRefs(dir)
	if err != nil {
		t.Fatalf("CheckMRefs: %v", err)
	}
	if len(vs) != 1 {
		t.Fatalf("expected 1 m-ref violation, got %d: %v", len(vs), vs)
	}
	if vs[0].Gate != "G1" || vs[0].Kind != "m-ref" {
		t.Errorf("unexpected violation: %+v", vs[0])
	}
}

func TestCheckMRefsCleanSource(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "STDX.m"),
		"STDX ;\n set x=$$fmt^STDFMT(1)\n quit\n")
	vs, err := CheckMRefs(dir)
	if err != nil {
		t.Fatalf("CheckMRefs: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("expected no violations, got %v", vs)
	}
}

func TestCheckMRefsSkipsDist(t *testing.T) {
	dir := t.TempDir()
	// A generated artifact under dist/ that mentions ^VSL must not be scanned.
	writeFile(t, filepath.Join(dir, "dist", "bundle.m"), " do x^VSLCFG\n")
	vs, err := CheckMRefs(dir)
	if err != nil {
		t.Fatalf("CheckMRefs: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("dist/ must be skipped, got %v", vs)
	}
}

// --- Check (integration of layer + checks) ----------------------------------

func TestCheckVLayerPassesTrivially(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dist", "v-contract.json"), `{"layer":"v"}`)
	// Even with a VSL ref present, a v-layer repo passes G1 (v → m allowed).
	writeFile(t, filepath.Join(dir, "src", "VSLX.m"), " do y^VSLCFG\n")
	rep, err := Check(dir, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if rep.Layer != LayerV {
		t.Errorf("layer: got %q, want v", rep.Layer)
	}
	if len(rep.Violations) != 0 {
		t.Errorf("v-layer must pass G1, got %v", rep.Violations)
	}
}

func TestCheckMLayerScansM(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dist", "repo.meta.json"), `{"layer":"m"}`)
	writeFile(t, filepath.Join(dir, "src", "STDX.m"), " set x=$$cfg^VSLCFG(1)\n")
	rep, err := Check(dir, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !rep.CheckedM {
		t.Error("expected CheckedM=true")
	}
	if len(rep.Violations) != 1 {
		t.Errorf("expected 1 violation, got %v", rep.Violations)
	}
}

// --- Check, Go arm (live `go list`, stdlib-only temp module) ----------------

func TestCheckMLayerGoArmClean(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/clean\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "main.go"),
		"package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hi\") }\n")
	writeFile(t, filepath.Join(dir, "dist", "repo.meta.json"), `{"layer":"m"}`)
	rep, err := Check(dir, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !rep.CheckedGo {
		t.Error("expected CheckedGo=true when go.mod is present")
	}
	if len(rep.Violations) != 0 {
		t.Errorf("stdlib-only module must be clean, got %v", rep.Violations)
	}
}

// --- G2: codePortion (comment-awareness) ------------------------------------

func TestCodePortion(t *testing.T) {
	cases := map[string]string{
		"\tdo FILE^DIE(x) ; call the filer": "\tdo FILE^DIE(x) ",
		"\t; do FILE^DIE(x)":                "\t",
		"\tset x=\"a;b\" ; tail":            "\tset x=\"a;b\" ",
		// A ';' inside a (doubled-quote) string is not a comment.
		"\tset x=\"q\"\" ; in string\"": "\tset x=\"q\"\" ; in string\"",
		"\tquit":                        "\tquit",
	}
	for in, want := range cases {
		if got := codePortion(in); got != want {
			t.Errorf("codePortion(%q) = %q, want %q", in, got, want)
		}
	}
}

// --- G2: CheckVistaSymbols (no VistA below the waterline) --------------------

func TestCheckVistaSymbolsFlagsCodeRef(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "STDX.m"),
		"STDX ;\nfiler() ;\n do FILE^DIE(\"\",a,b)\n quit\n")
	vs, err := CheckVistaSymbols(dir)
	if err != nil {
		t.Fatalf("CheckVistaSymbols: %v", err)
	}
	if len(vs) != 1 {
		t.Fatalf("expected 1 G2 violation, got %d: %v", len(vs), vs)
	}
	if vs[0].Gate != "G2" || vs[0].Kind != "vista-symbol" {
		t.Errorf("unexpected violation: %+v", vs[0])
	}
}

func TestCheckVistaSymbolsIgnoresComment(t *testing.T) {
	dir := t.TempDir()
	// STDMOCK's doc examples name "EN^DIE" as a mock target — comment only.
	writeFile(t, filepath.Join(dir, "src", "STDMOCK.m"),
		"STDMOCK ;\n ; doc: @example do register^STDMOCK(\"EN^DIE\",\"stub\")\n quit\n")
	vs, err := CheckVistaSymbols(dir)
	if err != nil {
		t.Fatalf("CheckVistaSymbols: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("comment mentions must not be flagged, got %v", vs)
	}
}

func TestCheckVistaSymbolsTrailingGuard(t *testing.T) {
	dir := t.TempDir()
	// ^DIETST is a test routine name, not FileMan ^DIE — must not match.
	writeFile(t, filepath.Join(dir, "src", "STDX.m"),
		"STDX ;\n do stub^DIETST\n quit\n")
	vs, err := CheckVistaSymbols(dir)
	if err != nil {
		t.Fatalf("CheckVistaSymbols: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("^DIETST must not match ^DIE, got %v", vs)
	}
}

func TestCheckVistaSymbolsGlobals(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "src", "STDX.m"),
		"STDX ;\n set a=^DPT(1,0)\n set b=$get(^DD(2))\n set c=^VA(200,0)\n set d=^XUSEC(\"K\",1)\n quit\n")
	vs, err := CheckVistaSymbols(dir)
	if err != nil {
		t.Fatalf("CheckVistaSymbols: %v", err)
	}
	if len(vs) != 4 {
		t.Errorf("expected 4 violations (DPT/DD/VA/XUSEC), got %d: %v", len(vs), vs)
	}
}

func TestCheckMLayerFlagsVistaSymbol(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dist", "repo.meta.json"), `{"layer":"m"}`)
	writeFile(t, filepath.Join(dir, "src", "STDX.m"), " do FILE^DIE(\"\")\n")
	rep, err := Check(dir, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	var g2 int
	for _, v := range rep.Violations {
		if v.Gate == "G2" {
			g2++
		}
	}
	if g2 != 1 {
		t.Errorf("expected 1 G2 violation in report, got %d: %v", g2, rep.Violations)
	}
}

func TestCheckVLayerSkipsVistaSymbols(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dist", "v-contract.json"), `{"layer":"v"}`)
	// VistA symbols are expected above the waterline — v-layer passes.
	writeFile(t, filepath.Join(dir, "src", "VSLX.m"), " do FILE^DIE(\"\")\n")
	rep, err := Check(dir, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(rep.Violations) != 0 {
		t.Errorf("v-layer must pass G2, got %v", rep.Violations)
	}
}

// --- helpers -----------------------------------------------------------------

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func count(xs []string, want string) int {
	n := 0
	for _, x := range xs {
		if x == want {
			n++
		}
	}
	return n
}
