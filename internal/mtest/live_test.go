package mtest_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
	"github.com/vista-cloud-dev/m-cli/internal/mtest"
)

// TestLiveYDB runs a real suite against the m-test-engine YDB container through
// the docker transport — the end-to-end engine-bound path (gate G4). It is
// opt-in so CI (which has no engine container) skips it:
//
//	M_TEST_LIVE=1 M_STDLIB_SRC=$HOME/m-dev-tools/m-stdlib/src go test ./internal/mtest/ -run TestLiveYDB
func TestLiveYDB(t *testing.T) {
	if os.Getenv("M_TEST_LIVE") == "" {
		t.Skip("set M_TEST_LIVE=1 (+ M_STDLIB_SRC) to run the live YDB integration test")
	}
	stdlib := os.Getenv("M_STDLIB_SRC")
	if stdlib == "" {
		t.Skip("set M_STDLIB_SRC to the m-stdlib src dir (provides ^STDASSERT)")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	container := os.Getenv("M_TEST_ENGINE_CONTAINER")
	if container == "" {
		container = "m-test-engine"
	}
	ctx := context.Background()

	stageDir := "/m-work/m-test-itest"
	files := []string{"testdata/SAMPLETST.m"}
	entries, err := os.ReadDir(stdlib)
	if err != nil {
		t.Fatalf("read M_STDLIB_SRC: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".m" {
			files = append(files, filepath.Join(stdlib, e.Name()))
		}
	}
	if err := engine.DockerStage(ctx, container, stageDir, files); err != nil {
		t.Fatalf("stage: %v", err)
	}
	defer engine.DockerUnstage(ctx, container, stageDir)

	eng := engine.New(engine.YDB, engine.Options{Runner: engine.DockerRunner(container, stageDir)})
	r, err := mtest.RunSuite(ctx, eng, mtest.TestSuite{Name: "SAMPLETST", Path: "testdata/SAMPLETST.m"})
	if err != nil {
		t.Fatalf("RunSuite: %v", err)
	}
	if !r.OK || r.Summary.Passed != 2 || r.Summary.Failed != 0 {
		t.Errorf("live result = %+v, want ok 2 passed 0 failed; stdout:\n%s", r.Summary, r.Stdout)
	}
}
