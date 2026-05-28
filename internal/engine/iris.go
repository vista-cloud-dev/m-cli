package engine

import "context"

// IrisEngine runs M on InterSystems IRIS via the `iris` binary (the VA target
// engine). Routine source lives in IRIS.DAT, so EnsureLoaded imports a .mac
// from the irissync mirror before it can run.
type IrisEngine struct {
	bin       string
	instance  string
	namespace string
	run       Runner
}

// Kind implements Engine.
func (e *IrisEngine) Kind() Kind { return IRIS }

// EnsureLoaded compiles a routine file into the namespace via
// $SYSTEM.OBJ.Load(path,"ck") — the IRIS analog of YDB's auto-compile.
func (e *IrisEngine) EnsureLoaded(ctx context.Context, path string) error {
	_, err := e.RunXCmd(ctx, `do $SYSTEM.OBJ.Load("`+path+`","ck")`)
	return err
}

// RunRoutine runs an entryref by piping `do <entryref>  halt` to an interactive
// `iris session` (more reliable than passing the routine on argv). args are
// ignored for now (IRIS argument passing differs from YDB's $ZCMDLINE).
func (e *IrisEngine) RunRoutine(ctx context.Context, entryref string, _ ...string) (Result, error) {
	argv := []string{e.bin, "session", e.instance, "-U", e.namespace}
	return e.run(ctx, argv, "do "+entryref+"\nhalt\n")
}

// RunXCmd runs a one-off M command by piping it (plus halt) to an interactive
// `iris session` over stdin.
func (e *IrisEngine) RunXCmd(ctx context.Context, mcmd string) (Result, error) {
	argv := []string{e.bin, "session", e.instance, "-U", e.namespace}
	return e.run(ctx, argv, mcmd+"\nhalt\n")
}

// RunScript pipes a multi-line script to an interactive `iris session` (the
// script should end with `halt`; one is appended if absent).
func (e *IrisEngine) RunScript(ctx context.Context, script string) (Result, error) {
	argv := []string{e.bin, "session", e.instance, "-U", e.namespace}
	return e.run(ctx, argv, script)
}
