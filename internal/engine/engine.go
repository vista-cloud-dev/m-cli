// Package engine is the runtime adapter — the only engine-bound code in m-cli
// (spec §8). Everything else (fmt/lint/lsp) is engine-neutral source work; this
// package is where "run M" lives, and the only place the YottaDB-vs-IRIS
// difference appears. `m test`/`m coverage`/the `m watch` run-half drive it.
//
// The YDB-vs-IRIS difference is just the *invocation shim* — the M code itself
// (TESTRUN.m, ^STDASSERT, …) is pure M and runs unchanged on both. Each Engine
// builds the right command line; how that command actually executes (locally,
// in a docker container, over SSH) is the injectable Runner seam, so this
// package is testable without a live engine and transports can be added later.
package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
)

// Kind is the M engine implementation.
type Kind string

const (
	YDB  Kind = "ydb"
	IRIS Kind = "iris"
)

// Result is the outcome of running an M command.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Runner executes argv (with optional stdin) and returns the result. It is the
// transport seam: LocalRunner runs via os/exec; docker/SSH transports and tests
// substitute their own.
type Runner func(ctx context.Context, argv []string, stdin string) (Result, error)

// Engine is the runtime adapter. EnsureLoaded makes a routine file known to the
// engine (a no-op on YDB, an OBJ.Load on IRIS); RunRoutine runs an entryref;
// RunXCmd runs a one-off M command line.
type Engine interface {
	Kind() Kind
	EnsureLoaded(ctx context.Context, path string) error
	RunRoutine(ctx context.Context, entryref string, args ...string) (Result, error)
	RunXCmd(ctx context.Context, mcmd string) (Result, error)
}

// Options configure a constructed engine (defaults are filled by New).
type Options struct {
	Runner    Runner // transport; default LocalRunner
	YdbBin    string // default "ydb"
	IrisBin   string // default "iris"
	Instance  string // IRIS instance name (default "IRIS")
	Namespace string // IRIS namespace (default "USER")
}

// New builds the Engine for kind with opts (zero values defaulted).
func New(kind Kind, opts Options) Engine {
	if opts.Runner == nil {
		opts.Runner = LocalRunner
	}
	if kind == IRIS {
		return &IrisEngine{
			bin:       orDefault(opts.IrisBin, "iris"),
			instance:  orDefault(opts.Instance, "IRIS"),
			namespace: orDefault(opts.Namespace, "USER"),
			run:       opts.Runner,
		}
	}
	return &YdbEngine{bin: orDefault(opts.YdbBin, "ydb"), run: opts.Runner}
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// Config carries the inputs to the engine-selection consensus (spec §2.1).
type Config struct {
	Flag          string // --engine value ("" if unset)
	Env           string // $M_ENGINE
	IrisOnPath    bool   // `iris` resolvable on $PATH
	IscInstallDir string // $ISC_PACKAGE_INSTALLDIR
}

// DetectConfig reads the ambient selection inputs, layering flag on top.
func DetectConfig(flag string) Config {
	_, err := exec.LookPath("iris")
	return Config{
		Flag:          flag,
		Env:           os.Getenv("M_ENGINE"),
		IrisOnPath:    err == nil,
		IscInstallDir: os.Getenv("ISC_PACKAGE_INSTALLDIR"),
	}
}

// Resolve applies the §2.1 consensus: --engine → $M_ENGINE → heuristic
// ($ISC_PACKAGE_INSTALLDIR / iris on PATH ⇒ iris) → default ydb. explicit is
// false only for the bare default (no flag/env/heuristic) — engine-bound
// commands must refuse (exit 4) when !explicit rather than assume ydb.
func Resolve(c Config) (kind Kind, explicit bool, err error) {
	pick := func(s string) (Kind, error) {
		switch Kind(s) {
		case YDB, IRIS:
			return Kind(s), nil
		default:
			return "", fmt.Errorf("engine: unknown engine %q (want ydb or iris)", s)
		}
	}
	switch {
	case c.Flag != "":
		k, err := pick(c.Flag)
		return k, true, err
	case c.Env != "":
		k, err := pick(c.Env)
		return k, true, err
	case c.IscInstallDir != "" || c.IrisOnPath:
		return IRIS, true, nil
	default:
		return YDB, false, nil
	}
}

// LocalRunner runs argv on the host via os/exec. A non-zero engine exit is
// reported in Result.ExitCode (not as a Go error); a Go error means the command
// could not be run at all (e.g. binary not found).
func LocalRunner(ctx context.Context, argv []string, stdin string) (Result, error) {
	if len(argv) == 0 {
		return Result{}, errors.New("engine: empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin != "" {
		cmd.Stdin = bytes.NewReader([]byte(stdin))
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	return res, err
}
