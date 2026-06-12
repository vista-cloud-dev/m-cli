package driver

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// fakeCmd records the last invocation and returns canned streams.
type fakeCmd struct {
	name   string
	args   []string
	stdout []byte
	stderr []byte
	exit   int
	err    error
}

func (f *fakeCmd) run(_ context.Context, name string, args []string) (stdout, stderr []byte, exit int, err error) {
	f.name, f.args = name, args
	return f.stdout, f.stderr, f.exit, f.err
}

// envBytes builds a clikit-style JSON envelope (the wire shape the client parses).
func envBytes(t *testing.T, command string, ok bool, exit int, data any, eng *mdriver.EngineError) []byte {
	t.Helper()
	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("marshal data: %v", err)
	}
	env := map[string]any{
		"schemaVersion": "1.0", "command": command, "ok": ok, "exit": exit,
		"data": json.RawMessage(raw),
	}
	if eng != nil {
		env["engineError"] = eng
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal env: %v", err)
	}
	return b
}

func TestClient_Status_ArgvAndParse(t *testing.T) {
	st := mdriver.Status{Transport: "remote", Running: true, Healthy: true, Version: "IRIS for UNIX 2026.1"}
	f := &fakeCmd{stdout: envBytes(t, "lifecycle status", true, 0, st, nil)}
	c := NewClient("/bin/m-iris", "iris", "remote", []string{"--base-url", "X"}, f.run)

	got, err := c.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if !got.Healthy || got.Version != st.Version {
		t.Errorf("status = %+v, want healthy + version", got)
	}
	if f.name != "/bin/m-iris" {
		t.Errorf("bin = %q", f.name)
	}
	want := []string{"lifecycle", "status", "--transport", "remote", "--base-url", "X", "--output", "json"}
	if !reflect.DeepEqual(f.args, want) {
		t.Errorf("argv = %v\nwant %v", f.args, want)
	}
}

func TestClient_ExecEval_StdoutSuccess(t *testing.T) {
	r := mdriver.ExecResult{Stdout: "HI", Status: 0}
	f := &fakeCmd{stdout: envBytes(t, "exec eval", true, 0, r, nil)}
	c := NewClient("m-ydb", "ydb", "local", nil, f.run)

	got, err := c.ExecEval(context.Background(), `w "HI"`)
	if err != nil {
		t.Fatalf("ExecEval: %v", err)
	}
	if got.Stdout != "HI" {
		t.Errorf("stdout = %q", got.Stdout)
	}
	if got.EngineError != nil {
		t.Errorf("unexpected engineError: %+v", got.EngineError)
	}
	want := []string{"exec", "eval", `w "HI"`, "--transport", "local", "--output", "json"}
	if !reflect.DeepEqual(f.args, want) {
		t.Errorf("argv = %v\nwant %v", f.args, want)
	}
}

// On an engine fault the driver emits the envelope (with engineError) to STDERR
// and exits non-zero; the client must read stderr and surface engineError as
// DATA (not a Go error) so callers can render a RED-with-cause result.
func TestClient_ExecEval_EngineErrorFromStderr(t *testing.T) {
	eng := &mdriver.EngineError{Mnemonic: "<NOROUTINE>", Text: "no such routine", Line: 12, Routine: "ZZZ"}
	f := &fakeCmd{
		stderr: envBytes(t, "exec eval", false, 5, mdriver.ExecResult{Status: 5}, eng),
		exit:   5,
	}
	c := NewClient("m-ydb", "ydb", "local", nil, f.run)

	got, err := c.ExecEval(context.Background(), "do ^ZZZ")
	if err != nil {
		t.Fatalf("engine fault must be data, not a Go error: %v", err)
	}
	if got.EngineError == nil || got.EngineError.Mnemonic != "<NOROUTINE>" {
		t.Fatalf("engineError = %+v, want <NOROUTINE>", got.EngineError)
	}
}

func TestClient_LaunchError(t *testing.T) {
	f := &fakeCmd{err: errors.New("exec: \"m-ydb\": executable file not found")}
	c := NewClient("m-ydb", "ydb", "local", nil, f.run)
	if _, err := c.Status(context.Background()); err == nil {
		t.Error("a launch failure must be a Go error")
	}
}

func TestClient_BadEnvelope(t *testing.T) {
	f := &fakeCmd{stdout: []byte("not json"), exit: 0}
	c := NewClient("m-ydb", "ydb", "local", nil, f.run)
	if _, err := c.Status(context.Background()); err == nil {
		t.Error("non-JSON output must be a Go error")
	}
}
