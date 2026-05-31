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
