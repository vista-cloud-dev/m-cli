package mtest

import (
	"context"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
)

// RunResult is the outcome of running one suite.
type RunResult struct {
	Suite    string
	Summary  Summary
	OK       bool
	ExitCode int
	Stdout   string
}

// RunSuite loads (IRIS) and runs the suite's entry routine ^SUITE through the
// engine, then parses its ^STDASSERT/TESTRUN output. A suite is ok only when
// the parsed summary is ok AND the engine process exited 0 (a crash mid-suite
// must not read as a pass).
func RunSuite(ctx context.Context, eng engine.Engine, s TestSuite) (RunResult, error) {
	if err := eng.EnsureLoaded(ctx, s.Path); err != nil {
		return RunResult{Suite: s.Name}, err
	}
	res, err := eng.RunRoutine(ctx, "^"+s.Name)
	if err != nil {
		return RunResult{Suite: s.Name}, err
	}
	summary := ParseOutput(res.Stdout)
	return RunResult{
		Suite:    s.Name,
		Summary:  summary,
		OK:       summary.OK && res.ExitCode == 0,
		ExitCode: res.ExitCode,
		Stdout:   res.Stdout,
	}, nil
}

// Run runs every suite in order and returns the per-suite results. It stops at
// the first engine error (e.g. the engine is unreachable), returning what ran.
func Run(ctx context.Context, eng engine.Engine, suites []TestSuite) ([]RunResult, error) {
	out := make([]RunResult, 0, len(suites))
	for _, s := range suites {
		r, err := RunSuite(ctx, eng, s)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}
