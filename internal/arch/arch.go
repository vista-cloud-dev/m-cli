// Package arch implements the m/v waterline gates — the machine-checkable
// boundary between the engine-neutral `m` layer and the VistA-specific `v`
// layer (see docs/background/m-v-waterline-adr.md in the org `docs` repo).
//
// This stage ships G1 — dependency-direction — the core invariant: dependency
// flows one way, v → m, never the reverse. A repo declares its layer in a
// committed meta artifact ("layer": "m"|"v"); the gate then asserts that an
// `m`-layer repo's Go dependency closure contains no `vista-cloud-dev/v-*`
// module, and that its M source references no `VSL*` (v-layer) routine. A
// `v`-layer repo passes G1 trivially (v → m is allowed).
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

// vRoutineRef matches a reference to a v-layer (VSL*) M routine in any call
// form — ^VSLCFG, $$tag^VSLCFG, do x^VSLCFG — since all contain "^VSL".
var vRoutineRef = regexp.MustCompile(`\^VSL[A-Z0-9]*`)

// Violation is one G1 finding — a dependency that crosses the waterline the
// wrong way (m → v).
type Violation struct {
	Gate   string `json:"gate"`   // "G1"
	Kind   string `json:"kind"`   // "go-dep" | "m-ref"
	Source string `json:"source"` // offending module path or file:line
	Detail string `json:"detail"` // human-readable explanation
}

// Report is the full G1 result for one repo.
type Report struct {
	Layer      Layer       `json:"layer"`
	CheckedGo  bool        `json:"checkedGo"`
	CheckedM   bool        `json:"checkedM"`
	Violations []Violation `json:"violations"`
}

// metaCandidates are the committed meta artifacts, in priority order, that may
// carry the repo's "layer" declaration (ADR §3.1).
var metaCandidates = []string{
	filepath.Join("dist", "repo.meta.json"),
	filepath.Join("dist", "v-contract.json"),
	"repo.meta.json", // repos whose dist/ is gitignored (e.g. m-cli)
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
	return "", fmt.Errorf(`no "layer" declared — add it to dist/repo.meta.json or dist/v-contract.json, or pass --layer`)
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

// CheckMRefs scans the .m source under root for references to v-layer (VSL*)
// routines — the M-side m → v G1 violation. Generated/vendored trees are
// skipped (dist, vendor, .git, node_modules).
func CheckMRefs(root string) ([]Violation, error) {
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
			if m := vRoutineRef.FindString(line); m != "" {
				vs = append(vs, Violation{
					Gate: "G1", Kind: "m-ref",
					Source: fmt.Sprintf("%s:%d", rel, i+1),
					Detail: fmt.Sprintf("m-layer routine references v-layer routine %s", m),
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
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
	if layer == LayerV {
		return rep, nil
	}
	// Go dependency-direction (only when the repo is a Go module).
	if _, statErr := os.Stat(filepath.Join(root, "go.mod")); statErr == nil {
		mods, err := goListModules(root)
		if err != nil {
			return rep, err
		}
		rep.CheckedGo = true
		rep.Violations = append(rep.Violations, vViolations(mods)...)
	}
	// M-side dependency-direction (STD* → VSL*).
	mvs, err := CheckMRefs(root)
	if err != nil {
		return rep, err
	}
	rep.CheckedM = true
	rep.Violations = append(rep.Violations, mvs...)
	return rep, nil
}
