package harness

import (
	"context"
	"strings"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
)

// Trigger invokes the resident orchestrator run^STDHARN over the engine adapter
// and returns the raw result frame (design §3.1, the CLI trigger path). It is
// pure delegation — the scope (suite routine names) is passed straight to
// run^STDHARN and the frame comes back on the engine's stdout. The same frame
// travels over T.1's WebSocket transport unchanged.
//
// It drives run^STDHARN through RunScript (direct mode) rather than RunRoutine
// so it is engine-portable: the YDB-only RUN^STDHARN entry reads $ZCMDLINE,
// which IRIS lacks, whereas passing scope as an M argument works on both.
//
// The routines (STDHARN, STDASSERT, the suites) must already be available to the
// engine — staged/loaded by the caller, exactly as the host-orchestrated path
// arranges them. Suite names are routine names (no quoting hazard).
func Trigger(ctx context.Context, eng engine.Engine, scope []string) (string, error) {
	script := "do run^STDHARN(\"" + strings.Join(scope, " ") + "\")\nhalt\n"
	res, err := eng.RunScript(ctx, script)
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}

// TriggerCoverage invokes cov^STDHARN, which runs the scope under the IRIS line
// monitor over the named routines and adds a raw ##MON block to the frame (the
// host joins it via mcov.FromMonitor). On YDB the ##MON block is empty by design
// (YDB coverage stays the host-side view "TRACE" path); resident coverage is the
// IRIS tier.
func TriggerCoverage(ctx context.Context, eng engine.Engine, scope, routines []string) (string, error) {
	script := "do cov^STDHARN(\"" + strings.Join(scope, " ") + "\",\"" + strings.Join(routines, " ") + "\")\nhalt\n"
	res, err := eng.RunScript(ctx, script)
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}
