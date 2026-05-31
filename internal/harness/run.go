package harness

import (
	"context"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
	"github.com/vista-cloud-dev/m-cli/internal/mtest"
)

// RunResident triggers the resident orchestrator for the given suites and maps
// the framed result back into the SAME mtest.RunResult shape the file-side runner
// produces — so the two tiers reconcile (Reconcile) and render through one path.
// A suite is ok only when its parsed summary is ok AND its ##END exit is 0 (a
// mid-suite crash reads as a fail), identical to mtest.RunSuite. A structurally
// bad frame (no header, truncated stream) is an error.
func RunResident(ctx context.Context, eng engine.Engine, suites []mtest.TestSuite) ([]mtest.RunResult, error) {
	if len(suites) == 0 {
		return nil, nil
	}
	names := make([]string, len(suites))
	for i, s := range suites {
		names[i] = s.Name
	}
	frame, err := Trigger(ctx, eng, names)
	if err != nil {
		return nil, err
	}
	blocks, _, _, _, err := SplitFrame(frame)
	if err != nil {
		return nil, err
	}
	out := make([]mtest.RunResult, 0, len(blocks))
	for _, b := range blocks {
		s := mtest.ParseOutput(b.Body)
		out = append(out, mtest.RunResult{
			Suite:    b.Name,
			Summary:  s,
			OK:       s.OK && b.Exit == 0,
			ExitCode: b.Exit,
			Stdout:   b.Body,
		})
	}
	return out, nil
}
