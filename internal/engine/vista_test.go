package engine

import (
	"context"
	"encoding/json"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// vistaWith builds a VistaEngine over an mdriver.Client whose subprocess runner is
// faked: it returns the canned envelope for whatever verb is invoked.
func vistaWith(t *testing.T, kind Kind, command string, ok bool, exit int, data any, eng *mdriver.EngineError, toStderr bool) *VistaEngine {
	t.Helper()
	raw, _ := json.Marshal(data)
	env := map[string]any{"schemaVersion": "1.0", "command": command, "ok": ok, "exit": exit, "data": json.RawMessage(raw)}
	if eng != nil {
		env["engineError"] = eng
	}
	b, _ := json.Marshal(env)
	run := func(_ context.Context, _ string, _ []string) (stdout, stderr []byte, code int, err error) {
		if toStderr {
			return nil, b, exit, nil
		}
		return b, nil, exit, nil
	}
	cl := mdriver.NewClient("m-"+string(kind), string(kind), "remote", nil, run)
	return NewVista(kind, cl)
}

func TestVista_SatisfiesEngine(_ *testing.T) {
	var _ Engine = (*VistaEngine)(nil)
}

func TestVista_Probe_ReturnsVersion(t *testing.T) {
	st := mdriver.Status{Running: true, Healthy: true, Version: "IRIS for UNIX 2026.1"}
	e := vistaWith(t, IRIS, "lifecycle status", true, 0, st, nil, false)
	got, err := e.Probe(context.Background())
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if !got.Healthy || got.Version == "" {
		t.Errorf("probe = %+v, want healthy + version", got)
	}
	if e.Kind() != IRIS {
		t.Errorf("kind = %q, want iris", e.Kind())
	}
}

func TestVista_RunXCmd_Stdout(t *testing.T) {
	e := vistaWith(t, YDB, "exec eval", true, 0, mdriver.ExecResult{Stdout: "YottaDB r2.02", Status: 0}, nil, false)
	res, err := e.RunXCmd(context.Background(), "w $zv")
	if err != nil {
		t.Fatalf("runxcmd: %v", err)
	}
	if res.Stdout != "YottaDB r2.02" || res.ExitCode != 0 {
		t.Errorf("result = %+v", res)
	}
}

func TestVista_RunXCmd_EngineErrorToStderr(t *testing.T) {
	eng := &mdriver.EngineError{Mnemonic: "%YDB-E-LVUNDEF", Text: "undefined"}
	e := vistaWith(t, YDB, "exec eval", false, 5, mdriver.ExecResult{Status: 5}, eng, true)
	res, err := e.RunXCmd(context.Background(), "w undef")
	if err != nil {
		t.Fatalf("engine fault must be a result, not a Go error: %v", err)
	}
	if res.ExitCode != 5 || res.Stderr == "" {
		t.Errorf("result = %+v, want exit 5 + stderr cause", res)
	}
}

func TestVista_RunScript_Unsupported(t *testing.T) {
	e := vistaWith(t, YDB, "exec eval", true, 0, mdriver.ExecResult{}, nil, false)
	if _, err := e.RunScript(context.Background(), "set x=1\nwrite x"); err == nil {
		t.Error("RunScript must report unsupported over the driver transport")
	}
}
