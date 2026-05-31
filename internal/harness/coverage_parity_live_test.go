package harness_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
	"github.com/vista-cloud-dev/m-cli/internal/harness"
	"github.com/vista-cloud-dev/m-cli/internal/mcov"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// TestResidentCoverageParityIRIS is the coverage half of G4 on the resident IRIS
// tier: resident coverage (cov^STDHARN → raw ##MON block → mcov.FromMonitor) must
// roll up to the SAME ByFile coverage as the host-orchestrated IRIS path
// (mcov.Run → %Monitor). Both use the same monitor data and the same parse-tree
// denominator, so they agree by construction. Resident coverage is IRIS-only
// (YDB stays the host-side view "TRACE" path), so this test is IRIS-bound. Opt-in:
//
//	M_TEST_LIVE=1 M_STDLIB_SRC=$HOME/vista-cloud-dev/m-stdlib/src \
//	  M_IRIS_CONTAINER=vista-iris go test ./internal/harness/ -run TestResidentCoverageParityIRIS
func TestResidentCoverageParityIRIS(t *testing.T) {
	stdlib := liveStdlibSrc(t)
	container := os.Getenv("M_IRIS_CONTAINER")
	if container == "" {
		t.Skip("set M_IRIS_CONTAINER (e.g. vista-iris) to run the IRIS coverage parity test")
	}
	ns := envOr("M_IRIS_NAMESPACE", "USER")
	ctx := context.Background()
	stageDir := "/tmp/harness-cov-parity"

	// Cover STDMATH (a pure-logic module) exercised by STDMATHTST.
	covRoutine := "STDMATH"
	suite := "STDMATHTST"
	routinePath := filepath.Join(stdlib, covRoutine+".m")
	suitePath := filepath.Join(filepath.Dir(stdlib), "tests", suite+".m")

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
	files = append(files, suitePath)

	eng := engine.New(engine.IRIS, engine.Options{Runner: engine.DockerRunner(container, ""), Namespace: ns})
	if err := engine.IrisStageLoad(ctx, eng, container, stageDir, files); err != nil {
		t.Fatalf("iris stage: %v", err)
	}
	defer engine.DockerUnstage(ctx, container, stageDir)

	p, err := parse.New(ctx)
	if err != nil {
		t.Fatalf("parse.New: %v", err)
	}
	defer func() { _ = p.Close(ctx) }()

	// Host-orchestrated IRIS coverage (mcov.Run → %Monitor).
	hostRes, err := mcov.Run(ctx, p, eng, []string{routinePath}, []string{suite})
	if err != nil {
		t.Fatalf("host mcov.Run: %v", err)
	}
	hostBF := mcov.ByFile(hostRes)

	// Resident coverage: cov^STDHARN → ##MON block → mcov.FromMonitor.
	frame, err := harness.TriggerCoverage(ctx, eng, []string{suite}, []string{covRoutine})
	if err != nil {
		t.Fatalf("TriggerCoverage: %v", err)
	}
	_, _, mon, meta, err := harness.SplitFrame(frame)
	if err != nil {
		t.Fatalf("SplitFrame: %v\nframe:\n%s", err, frame)
	}
	if meta.Engine != "iris" {
		t.Errorf("frame engine=%q, want iris", meta.Engine)
	}
	if mon == "" {
		t.Fatalf("resident ##MON block is empty; frame:\n%s", frame)
	}
	resRes, err := mcov.FromMonitor(p, mon, []string{routinePath})
	if err != nil {
		t.Fatalf("FromMonitor: %v", err)
	}
	resBF := mcov.ByFile(resRes)

	// The test must be meaningful (some lines, some covered).
	if hostRes.Total() == 0 || hostRes.Covered() == 0 {
		t.Fatalf("host coverage trivial: %d/%d", hostRes.Covered(), hostRes.Total())
	}

	// G4 coverage: resident ByFile == host ByFile.
	if len(hostBF) != len(resBF) {
		t.Fatalf("ByFile len: host %d, resident %d", len(hostBF), len(resBF))
	}
	for i := range hostBF {
		if hostBF[i].Path != resBF[i].Path || hostBF[i].Covered != resBF[i].Covered || hostBF[i].Total != resBF[i].Total {
			t.Errorf("coverage parity MISMATCH:\n  host:     %+v\n  resident: %+v", hostBF[i], resBF[i])
		}
	}
	t.Logf("coverage parity OK: %s %d/%d lines (host == resident)", covRoutine, hostRes.Covered(), hostRes.Total())
}
