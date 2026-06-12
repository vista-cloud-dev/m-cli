// Package driver is m-cli's client for the m engine-driver contract: it reaches
// a live VistA (or any engine) by invoking an m-<engine> driver binary over the
// contract's subprocess + JSON-envelope seam (driver-contract.md §2) —
// `m-<engine> <axis> <verb> [args] --transport <t> [conn] --output json` — and
// parsing the one JSON envelope it writes. m-cli speaks only the neutral
// contract (§1, §11: "zero changes to m-cli" to add an engine); all vendor
// detail lives behind the binary, which the engine package's VistaEngine wraps.
//
// The drivers reach the engine themselves: m-ydb via local/docker/SSH, m-iris
// via Atelier REST — so this client never knows the wire, only the contract.
package driver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// CmdRunner runs the driver binary and returns its stdout, stderr, and process
// exit. Only a launch failure (binary missing, etc.) is a Go error; a non-zero
// engine/driver exit is a result. Injected so the client is testable without a
// real driver binary.
type CmdRunner func(ctx context.Context, name string, args []string) (stdout, stderr []byte, exit int, err error)

// Client drives one m-<engine> binary for one transport+connection.
type Client struct {
	Bin       string   // path to the m-<engine> binary
	Engine    string   // "ydb" | "iris" (for error messages)
	Transport string   // local | docker | remote
	ConnArgs  []string // extra connection flags (e.g. --container, --base-url); usually empty (driver reads M_<ENGINE>_* env)
	run       CmdRunner
}

// NewClient builds a driver client. A nil run uses the real subprocess runner.
func NewClient(bin, engine, transport string, connArgs []string, run CmdRunner) *Client {
	if run == nil {
		run = ExecRunner
	}
	return &Client{Bin: bin, Engine: engine, Transport: transport, ConnArgs: connArgs, run: run}
}

// envelope is the wire shape the driver writes (driver-contract.md §2). engineError
// is a sibling of data, set on a §7 fault.
type envelope struct {
	SchemaVersion string               `json:"schemaVersion"`
	Command       string               `json:"command"`
	OK            bool                 `json:"ok"`
	Exit          int                  `json:"exit"`
	Data          json.RawMessage      `json:"data"`
	Error         *envError            `json:"error,omitempty"`
	EngineError   *mdriver.EngineError `json:"engineError,omitempty"`
}

type envError struct {
	Code    string `json:"code"`
	Exit    int    `json:"exit"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

// call invokes a verb and returns the parsed envelope, decoding data into out
// (when non-nil). It reads the envelope from stdout, falling back to stderr —
// the drivers' success envelopes go to stdout (cc.Result), but a Fail-path
// envelope currently lands on stderr (a clikit deviation from §2's "one JSON
// envelope to stdout"; tolerated here, flagged for a clikit/conformance-v2 fix).
func (c *Client) call(ctx context.Context, out any, args ...string) (*envelope, error) {
	full := append([]string{}, args...)
	full = append(full, "--transport", c.Transport)
	full = append(full, c.ConnArgs...)
	full = append(full, "--output", "json")

	stdout, stderr, _, err := c.run(ctx, c.Bin, full)
	if err != nil {
		return nil, fmt.Errorf("driver %s: launch %s: %w", c.Engine, c.Bin, err)
	}

	env, perr := parseEnvelope(stdout)
	if perr != nil {
		// Fall back to stderr (Fail-path envelope) before giving up.
		if env2, perr2 := parseEnvelope(stderr); perr2 == nil {
			env = env2
		} else {
			return nil, fmt.Errorf("driver %s: no JSON envelope on stdout or stderr: %w", c.Engine, perr)
		}
	}
	if out != nil && len(env.Data) > 0 {
		if jerr := json.Unmarshal(env.Data, out); jerr != nil {
			return nil, fmt.Errorf("driver %s: decode %s data: %w", c.Engine, env.Command, jerr)
		}
	}
	return env, nil
}

func parseEnvelope(b []byte) (*envelope, error) {
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, errors.New("empty output")
	}
	var env envelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, err
	}
	if env.SchemaVersion == "" {
		return nil, errors.New("not a clikit envelope (no schemaVersion)")
	}
	return &env, nil
}

// Status runs `lifecycle status` — the reachability + identity probe (running,
// healthy, version). This is the engine-neutral way to prove a VistA is live and
// learn its version banner (the portable replacement for `W $ZV`, which only
// captures device output on YottaDB).
func (c *Client) Status(ctx context.Context) (mdriver.Status, error) {
	var s mdriver.Status
	_, err := c.call(ctx, &s, "lifecycle", "status")
	return s, err
}

// Caps fetches the driver's capability document.
func (c *Client) Caps(ctx context.Context) (mdriver.Caps, error) {
	var caps mdriver.Caps
	_, err := c.call(ctx, &caps, "meta", "caps")
	return caps, err
}

// ExecEval evaluates a single M command. A §7 engine fault is returned in
// ExecResult.EngineError (data), not as a Go error — only a transport/launch
// failure is a Go error.
func (c *Client) ExecEval(ctx context.Context, command string) (mdriver.ExecResult, error) {
	return c.exec(ctx, "eval", []string{command})
}

// ExecRun runs an entryref (args become $ZCMDLINE / the formallist).
func (c *Client) ExecRun(ctx context.Context, entryref string, args []string) (mdriver.ExecResult, error) {
	return c.exec(ctx, "run", append([]string{entryref}, args...))
}

func (c *Client) exec(ctx context.Context, verb string, rest []string) (mdriver.ExecResult, error) {
	var r mdriver.ExecResult
	env, err := c.call(ctx, &r, append([]string{"exec", verb}, rest...)...)
	if err != nil {
		return mdriver.ExecResult{}, err
	}
	if env.EngineError != nil {
		r.EngineError = env.EngineError
	}
	return r, nil
}

// Load stages + compiles routine source (exec load).
func (c *Client) Load(ctx context.Context, paths []string) (mdriver.LoadResult, error) {
	var r mdriver.LoadResult
	env, err := c.call(ctx, &r, append([]string{"exec", "load"}, paths...)...)
	if err != nil {
		return mdriver.LoadResult{}, err
	}
	if env.EngineError != nil {
		r.EngineError = env.EngineError
	}
	return r, nil
}

// ExecRunner is the production CmdRunner: it runs the driver binary, capturing
// stdout and stderr separately. A non-zero exit is a result; only a launch
// failure is an error.
func ExecRunner(ctx context.Context, name string, args []string) (stdout, stderr []byte, exit int, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	runErr := cmd.Run()
	var ee *exec.ExitError
	if errors.As(runErr, &ee) {
		return out.Bytes(), errb.Bytes(), ee.ExitCode(), nil
	}
	if runErr != nil {
		return out.Bytes(), errb.Bytes(), 0, runErr
	}
	return out.Bytes(), errb.Bytes(), 0, nil
}
