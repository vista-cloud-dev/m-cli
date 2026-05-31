package harness_test

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
	"github.com/vista-cloud-dev/m-cli/internal/harness"
	"github.com/vista-cloud-dev/m-cli/internal/mtest"
)

// scriptEngine is a fake engine whose RunScript returns a canned frame.
type scriptEngine struct{ frame string }

func (scriptEngine) Kind() engine.Kind                          { return engine.YDB }
func (scriptEngine) EnsureLoaded(context.Context, string) error { return nil }
func (scriptEngine) RunRoutine(context.Context, string, ...string) (engine.Result, error) {
	return engine.Result{}, nil
}
func (scriptEngine) RunXCmd(context.Context, string) (engine.Result, error) {
	return engine.Result{}, nil
}
func (e scriptEngine) RunScript(context.Context, string) (engine.Result, error) {
	return engine.Result{Stdout: e.frame}, nil
}

func TestRunResidentMapsFrameToRunResults(t *testing.T) {
	frame := "##M-HARNESS frame=1 tier=integration engine=iris ns=VEHU\n" +
		"##SUITE ^AINTTST\nResults: 2 tests  2 passed  0 failed\nAll tests passed.\n##END ^AINTTST exit=0\n" +
		"##SUITE ^BINTTST\n  FAIL  x\nResults: 1 tests  0 passed  1 failed\n1 test(s) FAILED.\n##END ^BINTTST exit=0\n" +
		"##END-HARNESS suites=2 pass=2 fail=1\n"
	eng := scriptEngine{frame: frame}
	suites := []mtest.TestSuite{{Name: "AINTTST"}, {Name: "BINTTST"}}

	got, err := harness.RunResident(context.Background(), eng, suites)
	if err != nil {
		t.Fatalf("RunResident: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2", len(got))
	}
	if !got[0].OK || got[0].Summary.Passed != 2 {
		t.Errorf("AINTTST = %+v, want ok 2 passed", got[0])
	}
	if got[1].OK || got[1].Summary.Failed != 1 {
		t.Errorf("BINTTST = %+v, want not-ok 1 failed", got[1])
	}
}

func TestRunResidentTruncatedFrameErrors(t *testing.T) {
	// No trailer ⇒ truncated; RunResident surfaces it rather than reporting a
	// partial pass.
	eng := scriptEngine{frame: "##M-HARNESS frame=1 tier=integration engine=iris ns=X\n" +
		"##SUITE ^AINTTST\nAll tests passed.\n##END ^AINTTST exit=0\n"}
	if _, err := harness.RunResident(context.Background(), eng, []mtest.TestSuite{{Name: "AINTTST"}}); err == nil {
		t.Error("want an error for a truncated frame, got nil")
	}
}

func TestRunResidentEmpty(t *testing.T) {
	got, err := harness.RunResident(context.Background(), scriptEngine{}, nil)
	if err != nil || got != nil {
		t.Errorf("RunResident(nil) = %v, %v; want nil, nil", got, err)
	}
}
