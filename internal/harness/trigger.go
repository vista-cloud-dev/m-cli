package harness

import (
	"context"
	"strings"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
)

// Trigger invokes the resident orchestrator RUN^STDHARN over the engine adapter
// and returns the raw result frame (design §3.1, the CLI trigger path). It is
// pure delegation — the scope (suite routine names) is passed as the engine's
// command line ($ZCMDLINE on YDB), and the frame comes back on the engine's
// stdout. No engine-specific logic beyond what the adapter abstracts; the same
// frame travels over T.1's WebSocket transport unchanged.
//
// The routines (STDHARN, STDASSERT, the suites) must already be available to the
// engine — staged/loaded by the caller, exactly as the host-orchestrated path
// arranges them.
func Trigger(ctx context.Context, eng engine.Engine, scope []string) (string, error) {
	res, err := eng.RunRoutine(ctx, "RUN^STDHARN", strings.Join(scope, " "))
	if err != nil {
		return "", err
	}
	return res.Stdout, nil
}
