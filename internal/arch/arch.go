// Package arch implements the m/v waterline gates — the machine-checkable
// boundary between the engine-neutral `m` layer and the VistA-specific `v`
// layer (see docs/background/m-v-waterline-adr.md in the org `docs` repo).
//
// It ships four gates:
//
//   - G1 — dependency-direction — the core invariant: dependency flows one way,
//     v → m, never the reverse. An `m`-layer repo's Go dependency closure must
//     contain no `vista-cloud-dev/v-*` module, and its M source must reference
//     no `VSL*` (v-layer) routine.
//   - G2 — forbidden-symbol (no VistA below the waterline): an `m`-layer `.m`
//     file's code must not reference a VistA-only symbol (FileMan/Kernel/KIDS:
//     ^DIC/^DIE/^DIK/^DIQ, ^DD(, ^DPT(, ^VA(, ^XUS*, ^XPD*). Comment-aware — a
//     symbol named only in a ';' comment (e.g. an STDMOCK doc example) is not a
//     reference.
//   - G3 — transport-monopoly: only m-driver-sdk may run a driver binary / build
//     the engine envelope. Any other repo's Go code naming a driver binary
//     ("m-ydb"/"m-iris") other than its own is hand-rolling transport — reach the
//     engine through mdriver.Client instead.
//   - G4 — seam-pin: a repo requiring m-driver-sdk must pin a tagged release in
//     go.mod — no `replace` to it, no pseudo-version (untagged commit).
//
// It also validates the repo's standardized meta artifact (Phase B item 1):
// root repo.meta.json (preferred, then dist/repo.meta.json) must carry id,
// layer, language, and verification_commands; layer must be "m" or "v".
//
// G1 and G2 apply to the m layer; G3, G4, and meta-validation are layer-agnostic
// (a v consumer also must not hand-roll transport and must seam-pin). A repo
// declares its layer in a committed meta artifact ("layer": "m"|"v"); a `v`-layer
// repo passes G1/G2 trivially.
package arch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Layer is a repo's side of the waterline.
type Layer string

const (
	// LayerM is the engine-neutral layer (runs on a bare M engine, no VistA).
	LayerM Layer = "m"
	// LayerV is the VistA-specific layer (needs Kernel/FileMan/KIDS).
	LayerV Layer = "v"
)

// vModulePrefix is the import-path prefix every VistA-specific Go module
// shares (v-pkg, v-cli, v-stdlib, …). An m-layer closure must not contain it.
const vModulePrefix = "github.com/vista-cloud-dev/v-"

// sdkModule is the one module allowed to run a driver binary / build the engine
// envelope — the transport monopoly (G3). Every other repo reaches the engine
// through its reference Client (mdriver.Client).
const sdkModule = "github.com/vista-cloud-dev/m-driver-sdk"

// driverBinaries are the engine-driver binary names. Outside the SDK, only the
// repo that *is* a given driver may name it; any other repo naming one is
// hand-rolling transport (G3).
var driverBinaries = []string{"m-ydb", "m-iris"}

// sdkPseudoVersion matches a Go pseudo-version — an untagged commit pin: a
// 14-digit UTC timestamp + 12-hex commit hash. A tagged require (vX.Y.Z) does
// not match. Used by G4 (seam-pin).
var sdkPseudoVersion = regexp.MustCompile(`\d{14}-[0-9a-f]{12}`)

// vRoutineRef matches a reference to a v-layer (VSL*) M routine in any call
// form — ^VSLCFG, $$tag^VSLCFG, do x^VSLCFG — since all contain "^VSL".
var vRoutineRef = regexp.MustCompile(`\^VSL[A-Z0-9]*`)

// vistaSymbols is the G2 deny-list: VistA-only symbols (FileMan/Kernel/KIDS)
// that must not appear in m-layer code. The FileMan-API patterns carry a
// trailing-delimiter guard `(?:[^A-Za-z0-9]|$)` so a longer routine name such
// as ^DIETST is not mistaken for ^DIE — Go's RE2 has no lookahead.
var vistaSymbols = []struct {
	name string
	re   *regexp.Regexp
}{
	{"^DIC/^DIE/^DIK/^DIQ (FileMan API)", regexp.MustCompile(`\^DI[CEKQ](?:[^A-Za-z0-9]|$)`)},
	{"^DD( (FileMan data dictionary)", regexp.MustCompile(`\^DD\(`)},
	{"^DPT( (patient file)", regexp.MustCompile(`\^DPT\(`)},
	{"^VA( (institution file)", regexp.MustCompile(`\^VA\(`)},
	{"^XUS* (Kernel security)", regexp.MustCompile(`\^XUS[A-Za-z0-9]*`)},
	{"^XPD* (KIDS)", regexp.MustCompile(`\^XPD[A-Za-z0-9]*`)},
}

// Violation is one gate finding — a waterline breach (G1–G4) or a meta-shape
// problem (META).
type Violation struct {
	Gate   string `json:"gate"`   // "G1" | "G2" | "G3" | "G4" | "META"
	Kind   string `json:"kind"`   // "go-dep" | "m-ref" | "vista-symbol" | "driver-ref" | "seam-replace" | "seam-untagged" | "meta-shape"
	Source string `json:"source"` // offending module path or file:line
	Detail string `json:"detail"` // human-readable explanation
}

// Report is the full waterline-gate result for one repo.
type Report struct {
	Layer       Layer       `json:"layer"`
	CheckedGo   bool        `json:"checkedGo"`   // G1 Go dependency closure
	CheckedM    bool        `json:"checkedM"`    // G1 m-ref + G2 forbidden-symbol
	CheckedG3   bool        `json:"checkedG3"`   // G3 transport-monopoly (driver refs)
	CheckedG4   bool        `json:"checkedG4"`   // G4 seam-pin (go.mod)
	CheckedMeta bool        `json:"checkedMeta"` // meta-artifact shape
	Violations  []Violation `json:"violations"`
}

// Meta is the standardized repo meta artifact (the schema item 1 validates).
// Required: id, layer, language, verification_commands. Optional fields
// (consumes, exposes — repo-defined object shapes) and descriptive fields
// (repo, role, license, …) are allowed and ignored here.
type Meta struct {
	ID                   string   `json:"id"`
	Layer                string   `json:"layer"`
	Language             []string `json:"language"`
	VerificationCommands []string `json:"verification_commands"`
}

// MetaProblem is one meta-shape finding (a missing or malformed field).
type MetaProblem struct {
	Field  string `json:"field"`
	Detail string `json:"detail"`
}

// metaCandidates are the committed meta artifacts, in priority order, that may
// carry the repo's "layer" declaration (ADR §3.1). Root repo.meta.json is the
// standard location and is read first; the dist/ forms are read for back-compat
// while repos migrate to root (Phase B item 1).
var metaCandidates = []string{
	"repo.meta.json",
	filepath.Join("dist", "repo.meta.json"),
	filepath.Join("dist", "v-contract.json"),
}

// metaFileCandidates are the repo.meta.json-shaped artifacts (root preferred,
// then dist/) that LoadMeta validates. v-contract.json is a differently-shaped
// per-domain artifact and is not validated here.
var metaFileCandidates = []string{
	"repo.meta.json",
	filepath.Join("dist", "repo.meta.json"),
}

// ResolveLayer determines the repo's declared layer. An explicit override
// ("m"/"v") wins; otherwise the top-level "layer" field of a known committed
// meta artifact is read (dist/repo.meta.json, then dist/v-contract.json).
func ResolveLayer(root, override string) (Layer, error) {
	if override != "" {
		switch Layer(override) {
		case LayerM, LayerV:
			return Layer(override), nil
		default:
			return "", fmt.Errorf("invalid layer override %q (want m or v)", override)
		}
	}
	for _, rel := range metaCandidates {
		body, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			continue
		}
		var meta struct {
			Layer string `json:"layer"`
		}
		if err := json.Unmarshal(body, &meta); err != nil {
			return "", fmt.Errorf("%s: %w", rel, err)
		}
		if meta.Layer == "" {
			continue
		}
		switch Layer(meta.Layer) {
		case LayerM, LayerV:
			return Layer(meta.Layer), nil
		default:
			return "", fmt.Errorf(`%s: invalid "layer" %q (want m or v)`, rel, meta.Layer)
		}
	}
	return "", fmt.Errorf(`no "layer" declared — add it to repo.meta.json (root, preferred), dist/repo.meta.json, or dist/v-contract.json, or pass --layer`)
}

// LoadMeta reads the repo's standardized meta artifact — root repo.meta.json
// preferred, then dist/repo.meta.json. found is false when neither exists (a
// repo that declares its layer only via dist/v-contract.json, or via --layer).
// A malformed JSON meta returns an error.
func LoadMeta(root string) (meta Meta, path string, found bool, err error) {
	for _, rel := range metaFileCandidates {
		body, readErr := os.ReadFile(filepath.Join(root, rel))
		if readErr != nil {
			continue
		}
		if err := json.Unmarshal(body, &meta); err != nil {
			return Meta{}, rel, true, fmt.Errorf("%s: %w", rel, err)
		}
		return meta, rel, true, nil
	}
	return Meta{}, "", false, nil
}

// ValidateMeta checks the meta against the standardized schema (Phase B item 1):
// id, layer, language, and verification_commands are required; layer must be
// "m" or "v"; consumes and exposes are optional. Returns one MetaProblem per
// violation (empty when clean).
func ValidateMeta(meta Meta) []MetaProblem {
	var ps []MetaProblem
	if strings.TrimSpace(meta.ID) == "" {
		ps = append(ps, MetaProblem{Field: "id", Detail: `required field "id" is missing or empty`})
	}
	switch Layer(meta.Layer) {
	case LayerM, LayerV:
	default:
		ps = append(ps, MetaProblem{Field: "layer", Detail: fmt.Sprintf(`"layer" must be "m" or "v" (got %q)`, meta.Layer)})
	}
	if len(meta.Language) == 0 {
		ps = append(ps, MetaProblem{Field: "language", Detail: `required field "language" is missing or empty`})
	}
	if len(meta.VerificationCommands) == 0 {
		ps = append(ps, MetaProblem{Field: "verification_commands", Detail: `required field "verification_commands" is missing or empty`})
	}
	return ps
}

// parseGoListDeps extracts the distinct module import paths from the streamed
// JSON objects emitted by `go list -deps -json ./...`.
func parseGoListDeps(stream []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(stream))
	seen := map[string]bool{}
	var mods []string
	for {
		var pkg struct {
			Module *struct {
				Path string `json:"Path"`
			} `json:"Module"`
		}
		if err := dec.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return nil, err
		}
		if pkg.Module == nil || pkg.Module.Path == "" || seen[pkg.Module.Path] {
			continue
		}
		seen[pkg.Module.Path] = true
		mods = append(mods, pkg.Module.Path)
	}
	return mods, nil
}

// vViolations flags any vista-cloud-dev/v-* module appearing in an m-layer
// dependency closure (the m → v G1 violation).
func vViolations(modulePaths []string) []Violation {
	var vs []Violation
	for _, p := range modulePaths {
		if strings.HasPrefix(p, vModulePrefix) {
			vs = append(vs, Violation{
				Gate: "G1", Kind: "go-dep", Source: p,
				Detail: "m-layer module depends on a v-layer module (v → m only)",
			})
		}
	}
	return vs
}

// goListModules runs `go list -deps -json ./...` in root and returns the
// distinct module paths in the dependency closure.
func goListModules(root string) ([]string, error) {
	cmd := exec.Command("go", "list", "-deps", "-json", "./...")
	cmd.Dir = root
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return parseGoListDeps(out.Bytes())
}

// forEachMLine walks the .m source under root and calls fn for every line.
// Generated/vendored trees are skipped (dist, vendor, .git, node_modules).
// rel is the path relative to root; lineNo is 1-based.
func forEachMLine(root string, fn func(rel string, lineNo int, line string)) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		if strings.ToLower(filepath.Ext(path)) != ".m" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for i, line := range strings.Split(string(body), "\n") {
			fn(rel, i+1, line)
		}
		return nil
	})
}

// codePortion returns the executable part of an M line — everything before the
// first ';' that is not inside a double-quoted string. M comments begin with
// ';'; a ';' inside a "..." literal (including a doubled-quote escape) is data,
// not a comment.
func codePortion(line string) string {
	inStr := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			inStr = !inStr
		case ';':
			if !inStr {
				return line[:i]
			}
		}
	}
	return line
}

// goCodePortion returns the code part of a Go line — everything before a "//"
// line comment that is not inside a "..." string. Backslash escapes inside a
// string are honored. (Driver-binary literals are double-quoted, so backtick
// raw strings and block comments need no special handling here.)
func goCodePortion(line string) string {
	inStr := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '\\':
			if inStr {
				i++ // skip the escaped character
			}
		case '"':
			inStr = !inStr
		case '/':
			if !inStr && i+1 < len(line) && line[i+1] == '/' {
				return line[:i]
			}
		}
	}
	return line
}

// goModulePath reads the module path from root/go.mod. ok is false when there
// is no go.mod (e.g. a pure-M repo).
func goModulePath(root string) (path string, ok bool) {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(body), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "module" {
			return f[1], true
		}
	}
	return "", false
}

// CheckMRefs scans the .m source under root for references to v-layer (VSL*)
// routines — the M-side m → v G1 violation.
func CheckMRefs(root string) ([]Violation, error) {
	var vs []Violation
	err := forEachMLine(root, func(rel string, lineNo int, line string) {
		if m := vRoutineRef.FindString(line); m != "" {
			vs = append(vs, Violation{
				Gate: "G1", Kind: "m-ref",
				Source: fmt.Sprintf("%s:%d", rel, lineNo),
				Detail: fmt.Sprintf("m-layer routine references v-layer routine %s", m),
			})
		}
	})
	return vs, err
}

// CheckVistaSymbols scans the code portion of the .m source under root for
// VistA-only symbols (the G2 violation — no VistA below the waterline).
// Comment text is ignored via codePortion.
func CheckVistaSymbols(root string) ([]Violation, error) {
	var vs []Violation
	err := forEachMLine(root, func(rel string, lineNo int, line string) {
		code := codePortion(line)
		for _, sym := range vistaSymbols {
			if sym.re.MatchString(code) {
				vs = append(vs, Violation{
					Gate: "G2", Kind: "vista-symbol",
					Source: fmt.Sprintf("%s:%d", rel, lineNo),
					Detail: fmt.Sprintf("m-layer source references VistA-only symbol %s", sym.name),
				})
			}
		}
	})
	return vs, err
}

// CheckDriverMonopoly scans the Go source under root for an exec of a driver
// binary other than the repo's own (selfName) — the G3 transport-monopoly
// violation (ADR §3.2: no `exec.Command(…, "m-ydb"/"m-iris", …)` outside the
// SDK). The driver literal must co-occur with an exec.Command/CommandContext
// call on the same code line, so the gate's own deny-list and string fixtures
// (which name the binaries but never exec them) do not trip it. Only
// m-driver-sdk may run a driver / build the envelope; every other consumer
// reaches the engine through mdriver.Client (engine name "ydb"/"iris", never the
// binary). Comment text is ignored (goCodePortion); generated/vendored trees are
// skipped. The SDK is exempt and is not scanned (the caller skips it).
func CheckDriverMonopoly(root, selfName string) ([]Violation, error) {
	var vs []Violation
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
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
		if strings.ToLower(filepath.Ext(path)) != ".go" {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		for i, line := range strings.Split(string(body), "\n") {
			code := goCodePortion(line)
			if !strings.Contains(code, "exec.Command") {
				continue
			}
			for _, bin := range driverBinaries {
				if bin == selfName {
					continue // a driver may run itself
				}
				if strings.Contains(code, `"`+bin+`"`) {
					vs = append(vs, Violation{
						Gate: "G3", Kind: "driver-ref",
						Source: fmt.Sprintf("%s:%d", rel, i+1),
						Detail: fmt.Sprintf("non-SDK repo execs driver binary %q — reach the engine via mdriver.Client", bin),
					})
				}
			}
		}
		return nil
	})
	return vs, err
}

// CheckSeamPin inspects root/go.mod for the seam-pin invariant (G4): a repo
// that requires m-driver-sdk must pin a *tagged* release — no `replace`
// directive to it and no pseudo-version (untagged commit) require. A repo with
// no go.mod, or one not depending on the SDK, passes trivially.
func CheckSeamPin(root string) ([]Violation, error) {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, nil
	}
	var vs []Violation
	inReplace := false
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(t, "replace ("):
			inReplace = true
			continue
		case inReplace && t == ")":
			inReplace = false
			continue
		}
		if !strings.Contains(t, sdkModule) {
			continue
		}
		// A replace directive to the SDK (single-line or inside a replace block).
		if (inReplace || strings.HasPrefix(t, "replace ")) && strings.Contains(t, "=>") {
			vs = append(vs, Violation{
				Gate: "G4", Kind: "seam-replace",
				Source: "go.mod",
				Detail: "m-driver-sdk pinned via a replace directive — require a tagged release instead",
			})
			continue
		}
		// Otherwise a require of the SDK — flag an untagged (pseudo-version) pin.
		if sdkPseudoVersion.MatchString(t) {
			vs = append(vs, Violation{
				Gate: "G4", Kind: "seam-untagged",
				Source: "go.mod",
				Detail: "m-driver-sdk pinned to a pseudo-version (untagged commit) — pin a tagged release",
			})
		}
	}
	return vs, nil
}

// Check resolves the repo layer and runs the applicable G1 checks. A v-layer
// repo passes trivially (v → m is allowed); an m-layer repo is checked on both
// the Go dependency closure (when a go.mod is present) and its M source.
func Check(root, override string) (Report, error) {
	layer, err := ResolveLayer(root, override)
	if err != nil {
		return Report{}, err
	}
	rep := Report{Layer: layer}
	selfMod, hasMod := goModulePath(root)

	// Meta-artifact shape (Phase B item 1). Validate when a repo.meta.json is
	// present (root preferred, then dist/); a malformed meta is a hard error.
	// A repo declaring its layer only via dist/v-contract.json or --layer has no
	// repo.meta.json to validate and is skipped.
	if meta, metaPath, found, mErr := LoadMeta(root); mErr != nil {
		return rep, mErr
	} else if found {
		rep.CheckedMeta = true
		for _, p := range ValidateMeta(meta) {
			rep.Violations = append(rep.Violations, Violation{
				Gate: "META", Kind: "meta-shape",
				Source: fmt.Sprintf("%s:%s", metaPath, p.Field),
				Detail: p.Detail,
			})
		}
	}

	// G1 + G2 apply to the m layer only (v → m, and VistA above the line, are
	// allowed).
	if layer == LayerM {
		// G1 Go dependency-direction (only when the repo is a Go module).
		if hasMod {
			mods, err := goListModules(root)
			if err != nil {
				return rep, err
			}
			rep.CheckedGo = true
			rep.Violations = append(rep.Violations, vViolations(mods)...)
		}
		// G1 M-side dependency-direction (STD* → VSL*).
		mvs, err := CheckMRefs(root)
		if err != nil {
			return rep, err
		}
		rep.CheckedM = true
		rep.Violations = append(rep.Violations, mvs...)
		// G2 forbidden-symbol (no VistA below the waterline).
		sym, err := CheckVistaSymbols(root)
		if err != nil {
			return rep, err
		}
		rep.Violations = append(rep.Violations, sym...)
	}

	// G3 transport-monopoly applies to every repo except the SDK itself, which
	// owns the transport and legitimately names every driver binary.
	if selfMod != sdkModule {
		selfName := ""
		if hasMod {
			selfName = selfMod[strings.LastIndex(selfMod, "/")+1:]
		}
		g3, err := CheckDriverMonopoly(root, selfName)
		if err != nil {
			return rep, err
		}
		rep.CheckedG3 = true
		rep.Violations = append(rep.Violations, g3...)
	}

	// G4 seam-pin applies to every repo (trivial for one not requiring the SDK).
	g4, err := CheckSeamPin(root)
	if err != nil {
		return rep, err
	}
	rep.CheckedG4 = true
	rep.Violations = append(rep.Violations, g4...)

	return rep, nil
}
