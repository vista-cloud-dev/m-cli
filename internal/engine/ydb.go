package engine

import "context"

// YdbEngine runs M on YottaDB via the `ydb` binary (the tooling-native engine).
type YdbEngine struct {
	bin   string
	run   Runner
	chset string // "" = inherit ambient $ydb_chset; "M"/"UTF-8" exported per-run
}

// Kind implements Engine.
func (e *YdbEngine) Kind() Kind { return YDB }

// cmd builds the argv for a `ydb` invocation, prepending `env ydb_chset=<chset>`
// when a charset is pinned. The `env` prefix sets the variable for the ydb
// process under both LocalRunner (os/exec) and DockerRunner (inside `bash -lc`,
// overriding the container's profile default) without widening the Runner seam.
func (e *YdbEngine) cmd(args ...string) []string {
	argv := append([]string{e.bin}, args...)
	if e.chset != "" {
		argv = append([]string{"env", "ydb_chset=" + e.chset}, argv...)
	}
	return argv
}

// EnsureLoaded is a no-op on YottaDB: routines compile on first reference
// ($ydb_routines auto-compile), so there is nothing to pre-load.
func (e *YdbEngine) EnsureLoaded(_ context.Context, _ string) error { return nil }

// RunRoutine runs an entryref via `ydb -run`. Extra args are passed through as
// $ZCMDLINE.
func (e *YdbEngine) RunRoutine(ctx context.Context, entryref string, args ...string) (Result, error) {
	argv := append(e.cmd("-run", entryref), args...)
	return e.run(ctx, argv, "")
}

// RunXCmd runs a one-off M command line via the %XCMD utility (which XECUTEs its
// $ZCMDLINE): `ydb -run %XCMD <mcmd>`.
func (e *YdbEngine) RunXCmd(ctx context.Context, mcmd string) (Result, error) {
	return e.run(ctx, e.cmd("-run", "%XCMD", mcmd), "")
}

// RunScript runs a multi-line script in YDB direct mode (`ydb -direct`), feeding
// the script on stdin. The script should end with `halt`.
func (e *YdbEngine) RunScript(ctx context.Context, script string) (Result, error) {
	return e.run(ctx, e.cmd("-direct"), script)
}
