package engine

import (
	"context"
	"fmt"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// VistaEngine is the driver-backed engine: it satisfies Engine by delegating
// every verb to an m-<engine> driver binary over the neutral contract (the
// m-driver-sdk reference Client), instead of building a yottadb/iris argv for an in-process
// Runner. This is how the m-cli runner reaches a live FOIA VistA on either engine
// behind one contract — the driver owns the wire (m-ydb local/docker/SSH, m-iris
// Atelier REST), so m-cli stays vendor-neutral (driver-contract §1, §11).
//
// It sits beside the in-process YdbEngine/IrisEngine (additive); the D3 cutover
// that retires those is a separate, conformance-gated step.
type VistaEngine struct {
	kind   Kind
	client *mdriver.Client
}

var _ Engine = (*VistaEngine)(nil)

// NewVista builds a driver-backed engine of kind (ydb|iris) over client.
func NewVista(kind Kind, client *mdriver.Client) *VistaEngine {
	return &VistaEngine{kind: kind, client: client}
}

// Kind reports the engine the driver targets (ydb or iris).
func (e *VistaEngine) Kind() Kind { return e.kind }

// EnsureLoaded stages + compiles a routine via `exec load`.
func (e *VistaEngine) EnsureLoaded(ctx context.Context, path string) error {
	r, err := e.client.Load(ctx, []string{path})
	if err != nil {
		return err
	}
	if r.EngineError != nil {
		return fmt.Errorf("load %s: %s %s", path, r.EngineError.Mnemonic, r.EngineError.Text)
	}
	return nil
}

// RunRoutine runs an entryref via `exec run`; args become $ZCMDLINE.
func (e *VistaEngine) RunRoutine(ctx context.Context, entryref string, args ...string) (Result, error) {
	r, err := e.client.ExecRun(ctx, entryref, args)
	if err != nil {
		return Result{}, err
	}
	return toResult(r), nil
}

// RunXCmd evaluates one M command via `exec eval`.
func (e *VistaEngine) RunXCmd(ctx context.Context, mcmd string) (Result, error) {
	r, err := e.client.ExecEval(ctx, mcmd)
	if err != nil {
		return Result{}, err
	}
	return toResult(r), nil
}

// RunScript is not available over the driver transport: the exec axis has no
// multi-line direct-mode verb (load/run/eval/abort only). Compound work runs as
// a staged routine + RunRoutine, not a stdin script.
func (e *VistaEngine) RunScript(_ context.Context, _ string) (Result, error) {
	return Result{}, fmt.Errorf("engine: RunScript is not supported over the VistA driver transport (stage a routine with EnsureLoaded, then RunRoutine)")
}

// Probe is the reachability + identity gate (T0.1): `lifecycle status`, carrying
// running/healthy/version. It is the portable cross-engine equivalent of running
// `W $ZV` and reading the banner — IRIS exec does not capture device output, so
// status (not eval) is the uniform way to confirm a VistA is live and identify it.
func (e *VistaEngine) Probe(ctx context.Context) (mdriver.Status, error) {
	return e.client.Status(ctx)
}

// toResult maps a contract ExecResult onto the engine Result; an engineError is
// folded into Stderr so existing callers see a non-empty failure detail.
func toResult(r mdriver.ExecResult) Result {
	res := Result{Stdout: r.Stdout, ExitCode: r.Status}
	if r.EngineError != nil {
		res.Stderr = r.EngineError.Mnemonic
		if r.EngineError.Text != "" {
			res.Stderr += " " + r.EngineError.Text
		}
	}
	return res
}
