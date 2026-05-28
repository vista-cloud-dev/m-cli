package engine

import "context"

// YdbEngine runs M on YottaDB via the `ydb` binary (the tooling-native engine).
type YdbEngine struct {
	bin string
	run Runner
}

// Kind implements Engine.
func (e *YdbEngine) Kind() Kind { return YDB }

// EnsureLoaded is a no-op on YottaDB: routines compile on first reference
// ($ydb_routines auto-compile), so there is nothing to pre-load.
func (e *YdbEngine) EnsureLoaded(_ context.Context, _ string) error { return nil }

// RunRoutine runs an entryref via `ydb -run`. Extra args are passed through as
// $ZCMDLINE.
func (e *YdbEngine) RunRoutine(ctx context.Context, entryref string, args ...string) (Result, error) {
	argv := append([]string{e.bin, "-run", entryref}, args...)
	return e.run(ctx, argv, "")
}

// RunXCmd runs a one-off M command line via the %XCMD utility (which XECUTEs its
// $ZCMDLINE): `ydb -run %XCMD <mcmd>`.
func (e *YdbEngine) RunXCmd(ctx context.Context, mcmd string) (Result, error) {
	return e.run(ctx, []string{e.bin, "-run", "%XCMD", mcmd}, "")
}

// RunScript runs a multi-line script in YDB direct mode (`ydb -direct`), feeding
// the script on stdin. The script should end with `halt`.
func (e *YdbEngine) RunScript(ctx context.Context, script string) (Result, error) {
	return e.run(ctx, []string{e.bin, "-direct"}, script)
}
