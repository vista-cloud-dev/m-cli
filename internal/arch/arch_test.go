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
	writeFile(t, filepath.Join(dir, "dist", "repo.meta.json"),
		`{"id":"tool:x","layer":"m","language":["m"],"verification_commands":["m test"]}`)
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
	writeFile(t, filepath.Join(dir, "dist", "repo.meta.json"),
		`{"id":"tool:clean","layer":"m","language":["go"],"verification_commands":["go test ./..."]}`)
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

// --- G3: CheckDriverMonopoly (transport monopoly) ---------------------------

func TestCheckDriverMonopolyFlagsForeignDriver(t *testing.T) {
	dir := t.TempDir()
	// A consumer must not name a driver binary — it reaches the engine via
	// mdriver.Client (engine name "ydb"/"iris", never the binary).
	writeFile(t, filepath.Join(dir, "internal", "x.go"),
		"package x\nimport \"os/exec\"\nfunc r() { _ = exec.Command(\"m-ydb\", \"meta\") }\n")
	vs, err := CheckDriverMonopoly(dir, "m-cli")
	if err != nil {
		t.Fatalf("CheckDriverMonopoly: %v", err)
	}
	if len(vs) != 1 || vs[0].Gate != "G3" || vs[0].Kind != "driver-ref" {
		t.Fatalf("expected 1 G3 driver-ref, got %v", vs)
	}
}

func TestCheckDriverMonopolyAllowsNameWithoutExec(t *testing.T) {
	dir := t.TempDir()
	// Naming a driver binary without exec'ing it is fine — this is what makes
	// the gate self-hosting (its own deny-list var names both binaries).
	writeFile(t, filepath.Join(dir, "x.go"),
		"package x\nvar driverBinaries = []string{\"m-ydb\", \"m-iris\"}\n")
	vs, err := CheckDriverMonopoly(dir, "m-cli")
	if err != nil {
		t.Fatalf("CheckDriverMonopoly: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("naming a driver without exec is allowed, got %v", vs)
	}
}

func TestCheckDriverMonopolyAllowsSelfExec(t *testing.T) {
	dir := t.TempDir()
	// The m-ydb driver may exec itself.
	writeFile(t, filepath.Join(dir, "main.go"),
		"package main\nimport \"os/exec\"\nfunc r() { _ = exec.Command(\"m-ydb\") }\n")
	vs, err := CheckDriverMonopoly(dir, "m-ydb")
	if err != nil {
		t.Fatalf("CheckDriverMonopoly: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("a driver exec'ing itself is allowed, got %v", vs)
	}
}

func TestCheckDriverMonopolyIgnoresComment(t *testing.T) {
	dir := t.TempDir()
	// Even an exec.Command named in a comment is not a real exec.
	writeFile(t, filepath.Join(dir, "x.go"),
		"package x\n// once did exec.Command(\"m-iris\", ...)\nvar y = 1\n")
	vs, err := CheckDriverMonopoly(dir, "m-cli")
	if err != nil {
		t.Fatalf("CheckDriverMonopoly: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("a driver exec named only in a comment is not a reference, got %v", vs)
	}
}

// --- G4: CheckSeamPin (seam pin) --------------------------------------------

func TestCheckSeamPinFlagsReplace(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"),
		"module example.com/c\n\ngo 1.26\n\nrequire github.com/vista-cloud-dev/m-driver-sdk v0.3.0\n\nreplace github.com/vista-cloud-dev/m-driver-sdk => ../m-driver-sdk\n")
	vs, err := CheckSeamPin(dir)
	if err != nil {
		t.Fatalf("CheckSeamPin: %v", err)
	}
	if len(vs) != 1 || vs[0].Gate != "G4" || vs[0].Kind != "seam-replace" {
		t.Fatalf("expected 1 G4 seam-replace, got %v", vs)
	}
}

func TestCheckSeamPinFlagsPseudoVersion(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"),
		"module example.com/c\n\ngo 1.26\n\nrequire github.com/vista-cloud-dev/m-driver-sdk v0.0.0-20260101000000-abcdef123456\n")
	vs, err := CheckSeamPin(dir)
	if err != nil {
		t.Fatalf("CheckSeamPin: %v", err)
	}
	if len(vs) != 1 || vs[0].Gate != "G4" || vs[0].Kind != "seam-untagged" {
		t.Fatalf("expected 1 G4 seam-untagged, got %v", vs)
	}
}

func TestCheckSeamPinCleanTagInBlock(t *testing.T) {
	dir := t.TempDir()
	// A tagged require inside a require ( ... ) block, no replace — clean.
	writeFile(t, filepath.Join(dir, "go.mod"),
		"module example.com/c\n\ngo 1.26\n\nrequire (\n\tgithub.com/alecthomas/kong v1.0.0\n\tgithub.com/vista-cloud-dev/m-driver-sdk v0.3.0\n)\n")
	vs, err := CheckSeamPin(dir)
	if err != nil {
		t.Fatalf("CheckSeamPin: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("a tagged require with no replace is clean, got %v", vs)
	}
}

func TestCheckSeamPinNoSdkDep(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/c\n\ngo 1.26\n")
	vs, err := CheckSeamPin(dir)
	if err != nil {
		t.Fatalf("CheckSeamPin: %v", err)
	}
	if len(vs) != 0 {
		t.Errorf("a repo not depending on the SDK passes G4, got %v", vs)
	}
}

// --- Check integration: G3/G4 are layer-agnostic ----------------------------

func TestCheckVLayerRunsSeamPin(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dist", "v-contract.json"), `{"layer":"v"}`)
	writeFile(t, filepath.Join(dir, "go.mod"),
		"module example.com/v\n\ngo 1.26\n\nrequire github.com/vista-cloud-dev/m-driver-sdk v0.3.0\n\nreplace github.com/vista-cloud-dev/m-driver-sdk => ../x\n")
	rep, err := Check(dir, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	var g4 int
	for _, v := range rep.Violations {
		if v.Gate == "G4" {
			g4++
		}
	}
	if g4 != 1 {
		t.Errorf("a v-layer repo must still run G4 seam-pin, got %v", rep.Violations)
	}
}

func TestCheckSdkExemptFromG3(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"),
		"module github.com/vista-cloud-dev/m-driver-sdk\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "dist", "repo.meta.json"), `{"layer":"m"}`)
	// The SDK legitimately execs every driver binary.
	writeFile(t, filepath.Join(dir, "client.go"),
		"package mdriver\n\nimport \"os/exec\"\n\nvar _ = exec.Command(\"m-ydb\")\n")
	rep, err := Check(dir, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	for _, v := range rep.Violations {
		if v.Gate == "G3" {
			t.Errorf("m-driver-sdk is exempt from G3, got %v", v)
		}
	}
}

// --- Item 1: meta-schema validation -----------------------------------------

func TestValidateMetaClean(t *testing.T) {
	m := Meta{ID: "tool:x", Layer: "m", Language: []string{"go"}, VerificationCommands: []string{"make test"}}
	if p := ValidateMeta(m); len(p) != 0 {
		t.Errorf("a complete meta has no problems, got %v", p)
	}
}

func TestValidateMetaMissingRequired(t *testing.T) {
	m := Meta{Layer: "m"} // missing id, language, verification_commands
	p := ValidateMeta(m)
	if len(p) != 3 {
		t.Errorf("expected 3 missing-field problems, got %d: %v", len(p), p)
	}
}

func TestValidateMetaBadLayer(t *testing.T) {
	m := Meta{ID: "x", Layer: "z", Language: []string{"go"}, VerificationCommands: []string{"t"}}
	p := ValidateMeta(m)
	if len(p) != 1 || p[0].Field != "layer" {
		t.Errorf("expected 1 layer problem, got %v", p)
	}
}

func TestValidateMetaOptionalFieldsAllowedAbsent(t *testing.T) {
	// consumes/exposes are optional — a meta without them is clean.
	m := Meta{ID: "x", Layer: "v", Language: []string{"m"}, VerificationCommands: []string{"m test"}}
	if p := ValidateMeta(m); len(p) != 0 {
		t.Errorf("optional fields may be absent, got %v", p)
	}
}

func TestLoadMetaPrefersRoot(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "repo.meta.json"),
		`{"id":"root","layer":"m","language":["go"],"verification_commands":["x"]}`)
	writeFile(t, filepath.Join(dir, "dist", "repo.meta.json"),
		`{"id":"dist","layer":"v","language":["m"],"verification_commands":["y"]}`)
	m, _, found, err := LoadMeta(dir)
	if err != nil || !found {
		t.Fatalf("LoadMeta: err=%v found=%v", err, found)
	}
	if m.ID != "root" {
		t.Errorf("root repo.meta.json must win, got id=%q", m.ID)
	}
}

func TestLoadMetaFallsToDist(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "dist", "repo.meta.json"),
		`{"id":"dist","layer":"m","language":["m"],"verification_commands":["y"]}`)
	m, _, found, err := LoadMeta(dir)
	if err != nil || !found {
		t.Fatalf("LoadMeta: err=%v found=%v", err, found)
	}
	if m.ID != "dist" {
		t.Errorf("got id=%q", m.ID)
	}
}

func TestLoadMetaAbsent(t *testing.T) {
	dir := t.TempDir()
	if _, _, found, err := LoadMeta(dir); err != nil || found {
		t.Errorf("no repo.meta.json → found=false, err=nil; got found=%v err=%v", found, err)
	}
}

func TestLoadMetaIgnoresObjectOptionalFields(t *testing.T) {
	dir := t.TempDir()
	// Real metas carry consumes/exposes as objects (not arrays); they must be
	// ignored, not cause an unmarshal error (regression: v-pkg/m-stdlib metas).
	writeFile(t, filepath.Join(dir, "repo.meta.json"),
		`{"id":"x","layer":"v","language":["go"],"verification_commands":["t"],"exposes":{"pkg":{"verbs":[]}},"consumes":{"sdk":"v0.3.0"}}`)
	m, _, found, err := LoadMeta(dir)
	if err != nil || !found {
		t.Fatalf("object-valued optional fields must be ignored: err=%v found=%v", err, found)
	}
	if p := ValidateMeta(m); len(p) != 0 {
		t.Errorf("clean meta with object optional fields, got %v", p)
	}
}

func TestCheckReportsMetaProblems(t *testing.T) {
	dir := t.TempDir()
	// Layer resolves (m) but the meta is missing the other required fields.
	writeFile(t, filepath.Join(dir, "repo.meta.json"), `{"layer":"m"}`)
	rep, err := Check(dir, "")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if !rep.CheckedMeta {
		t.Error("expected CheckedMeta=true")
	}
	var meta int
	for _, v := range rep.Violations {
		if v.Gate == "META" {
			meta++
		}
	}
	if meta == 0 {
		t.Errorf("expected META problems for an incomplete meta, got %v", rep.Violations)
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
