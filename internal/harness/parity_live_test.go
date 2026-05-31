package harness_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
	"github.com/vista-cloud-dev/m-cli/internal/harness"
	"github.com/vista-cloud-dev/m-cli/internal/mtest"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// Pure-logic suites (deterministic, no shared global state) so running them in
// one resident process matches one-process-per-suite file-side. Plus the
// deliberately-failing fixture, to prove FAIL-path parity through no-halt.
var paritySuites = []string{"STDMATHTST", "STDSTRTST", "STDSEMVERTST", "STDHEXTST", "PARITYFAILTST"}

// TestResidentParityYDB is stage 5.1's gate (G4): the SAME *TST suites must
// yield IDENTICAL mtest.Summary results whether run file-side (host-orchestrated,
// one process per suite) or resident (RUN^STDHARN, all suites in one process,
// framed). Portable pure-M makes this exercisable on YDB with no IRIS. Opt-in:
//
//	M_TEST_LIVE=1 M_STDLIB_SRC=$HOME/vista-cloud-dev/m-stdlib/src \
//	  go test ./internal/harness/ -run TestResidentParityYDB
func TestResidentParityYDB(t *testing.T) {
	stdlib := liveStdlibSrc(t)
	residentParity(t, engine.YDB, envOr("M_TEST_ENGINE_CONTAINER", "m-test-engine"), "", stdlib)
}

// TestResidentParityIRIS is the IRIS half of G4: the same parity but on the
// resident IRIS tier (the integration tier's real substrate). Opt-in:
//
//	M_TEST_LIVE=1 M_STDLIB_SRC=$HOME/vista-cloud-dev/m-stdlib/src \
//	  M_IRIS_CONTAINER=vista-iris go test ./internal/harness/ -run TestResidentParityIRIS
func TestResidentParityIRIS(t *testing.T) {
	stdlib := liveStdlibSrc(t)
	container := os.Getenv("M_IRIS_CONTAINER")
	if container == "" {
		t.Skip("set M_IRIS_CONTAINER (e.g. vista-iris) to run the IRIS parity test")
	}
	residentParity(t, engine.IRIS, container, envOr("M_IRIS_NAMESPACE", "USER"), stdlib)
}

func liveStdlibSrc(t *testing.T) string {
	t.Helper()
	if os.Getenv("M_TEST_LIVE") == "" {
		t.Skip("set M_TEST_LIVE=1 (+ M_STDLIB_SRC) to run the live parity test")
	}
	stdlib := os.Getenv("M_STDLIB_SRC")
	if stdlib == "" {
		t.Skip("set M_STDLIB_SRC to the m-stdlib src dir (provides STDHARN/STDASSERT)")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	return stdlib
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// residentParity runs the parity gate against one engine: stage the suites +
// their deps, run them file-side (one process per suite) and resident (one
// RUN^STDHARN), and assert identical per-suite Summaries.
func residentParity(t *testing.T, kind engine.Kind, container, namespace, stdlib string) {
	t.Helper()
	ctx := context.Background()
	// IRIS stages under /tmp (writable); YDB under the m-test-engine /m-work mount.
	stageDir := "/m-work/harness-parity"
	if kind == engine.IRIS {
		stageDir = "/tmp/harness-parity"
	}

	var files []string
	srcEntries, err := os.ReadDir(stdlib)
	if err != nil {
		t.Fatalf("read M_STDLIB_SRC: %v", err)
	}
	for _, e := range srcEntries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".m" {
			files = append(files, filepath.Join(stdlib, e.Name()))
		}
	}
	testsDir := filepath.Join(filepath.Dir(stdlib), "tests")
	var suiteFiles []string
	for _, n := range paritySuites {
		p := filepath.Join(testsDir, n+".m")
		if n == "PARITYFAILTST" {
			p = filepath.Join("testdata", n+".m")
		}
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("suite file missing: %s", p)
		}
		files = append(files, p)
		suiteFiles = append(suiteFiles, p)
	}

	var eng engine.Engine
	if kind == engine.IRIS {
		eng = engine.New(engine.IRIS, engine.Options{Runner: engine.DockerRunner(container, ""), Namespace: namespace})
		if err := engine.IrisStageLoad(ctx, eng, container, stageDir, files); err != nil {
			t.Fatalf("iris stage: %v", err)
		}
		defer engine.DockerUnstage(ctx, container, stageDir)
	} else {
		if err := engine.DockerStage(ctx, container, stageDir, files); err != nil {
			t.Fatalf("stage: %v", err)
		}
		defer engine.DockerUnstage(ctx, container, stageDir)
		eng = engine.New(engine.YDB, engine.Options{Runner: engine.DockerRunner(container, stageDir)})
	}

	// File-side tier: discover + run each suite host-orchestrated.
	p, err := parse.New(ctx)
	if err != nil {
		t.Fatalf("parse.New: %v", err)
	}
	defer func() { _ = p.Close(ctx) }()
	suites, err := mtest.Discover(p, suiteFiles)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	fileSide, err := mtest.Run(ctx, eng, suites)
	if err != nil {
		t.Fatalf("file-side Run: %v", err)
	}
	fileByName := map[string]mtest.Summary{}
	for _, r := range fileSide {
		fileByName[r.Suite] = r.Summary
	}

	// Resident tier: one RUN^STDHARN, split the frame, parse each block.
	frame, err := harness.Trigger(ctx, eng, paritySuites)
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	blocks, _, meta, err := harness.SplitFrame(frame)
	if err != nil {
		t.Fatalf("SplitFrame: %v\nframe:\n%s", err, frame)
	}
	if meta.Suites != len(paritySuites) {
		t.Errorf("trailer suites=%d, want %d", meta.Suites, len(paritySuites))
	}
	if meta.Engine != string(kind) {
		t.Errorf("frame engine=%q, want %q", meta.Engine, kind)
	}
	resByName := map[string]mtest.Summary{}
	for _, b := range blocks {
		resByName[b.Name] = mtest.ParseOutput(b.Body)
	}

	// G4: per-suite Summary must be identical across tiers.
	for _, n := range paritySuites {
		fs, ok := fileByName[n]
		if !ok {
			t.Errorf("%s: missing from file-side results", n)
			continue
		}
		rs, ok := resByName[n]
		if !ok {
			t.Errorf("%s: missing from resident frame", n)
			continue
		}
		if fs.Total != rs.Total || fs.Passed != rs.Passed || fs.Failed != rs.Failed || fs.OK != rs.OK {
			t.Errorf("%s parity MISMATCH on %s:\n  file-side: %d/%d/%d ok=%v\n  resident:  %d/%d/%d ok=%v",
				n, kind, fs.Total, fs.Passed, fs.Failed, fs.OK, rs.Total, rs.Passed, rs.Failed, rs.OK)
		}
	}
	// The failing fixture must read as a failure on both tiers (not a false pass).
	if rs := resByName["PARITYFAILTST"]; rs.OK || rs.Failed != 1 {
		t.Errorf("PARITYFAILTST resident (%s) = %+v, want a failure (Failed=1, OK=false)", kind, rs)
	}
}
