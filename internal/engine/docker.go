package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DockerRunner is a Runner transport that runs argv inside a running container
// via `docker exec -i <container> bash -lc …` — `bash -lc` loads the engine's
// shell env (e.g. YottaDB's $ydb_dist/$ydb_routines). When stageDir is
// non-empty it is prepended to the routine path so routines staged there
// resolve and auto-compile (the m-test-engine bind-mounts $HOME/m-work →
// /m-work, but /m-work is not on the default $ydb_routines).
func DockerRunner(container, stageDir string) Runner {
	return func(ctx context.Context, argv []string, stdin string) (Result, error) {
		inner := dockerEnvPrefix(stageDir) + shJoin(argv)
		dargv := []string{"docker", "exec", "-i", container, "bash", "-lc", inner}
		return LocalRunner(ctx, dargv, stdin)
	}
}

// dockerEnvPrefix is the `export …; ` shell prefix that puts the staged routine
// dir on the engine's routine search path, ahead of the engine's own resident
// routines. The base falls back to $gtmroutines when $ydb_routines is unset: a
// GT.M-configured VistA (e.g. the FOIA `vehu` image) sets gtmroutines, not
// ydb_routines — and once ydb_routines is set it OVERRIDES gtmroutines, so it
// must carry the resident base or the engine's own routines (XPAR, FileMan, …)
// disappear from the path and VistA-dependent suites fault. (gbldir needs no
// such handling: docker.go never sets ydb_gbldir, so the ambient gtmgbldir
// survives.) Empty stageDir → empty prefix (unchanged behavior).
func dockerEnvPrefix(stageDir string) string {
	if stageDir == "" {
		return ""
	}
	return `export ydb_routines="` + stageDir + ` ${ydb_routines:-$gtmroutines}"; `
}

// DockerStage creates stageDir inside the container and copies files into it
// (via `docker cp`, which works regardless of bind-mount ownership on the host).
func DockerStage(ctx context.Context, container, stageDir string, files []string) error {
	res, err := LocalRunner(ctx, []string{"docker", "exec", container, "mkdir", "-p", stageDir}, "")
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf("engine: mkdir %s in %s: %s", stageDir, container, strings.TrimSpace(res.Stderr))
	}
	for _, f := range files {
		dst := container + ":" + stageDir + "/" + filepath.Base(f)
		res, err := LocalRunner(ctx, []string{"docker", "cp", f, dst}, "")
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("engine: docker cp %s → %s: %s", f, dst, strings.TrimSpace(res.Stderr))
		}
	}
	return nil
}

// DockerUnstage removes stageDir from the container (best-effort).
func DockerUnstage(ctx context.Context, container, stageDir string) {
	_, _ = LocalRunner(ctx, []string{"docker", "exec", container, "rm", "-rf", stageDir}, "")
}

// IrisStageLoad wraps each routine file in the IRIS UDL header
// (`ROUTINE <stem> [Type=MAC]`), copies it into stageDir in the container, then
// compiles them all with one `$SYSTEM.OBJ.Load(...,"ck")` pass via eng (an
// IrisEngine over a docker transport). Unlike YottaDB, IRIS has no
// compile-from-path, so every routine — suites and deps like ^STDASSERT — must
// be loaded before any suite runs. Returns an error if a load reports failure.
func IrisStageLoad(ctx context.Context, eng Engine, container, stageDir string, files []string) error {
	tmp, err := os.MkdirTemp("", "m-iris-stage")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	if res, err := LocalRunner(ctx, []string{"docker", "exec", container, "mkdir", "-p", stageDir}, ""); err != nil {
		return err
	} else if res.ExitCode != 0 {
		return fmt.Errorf("engine: mkdir %s in %s: %s", stageDir, container, strings.TrimSpace(res.Stderr))
	}

	var loads []string
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		stem := strings.ToUpper(strings.TrimSuffix(filepath.Base(f), filepath.Ext(f)))
		wrapped := "ROUTINE " + stem + " [Type=MAC]\n" + string(src)
		local := filepath.Join(tmp, stem+".mac")
		if err := os.WriteFile(local, []byte(wrapped), 0o644); err != nil {
			return err
		}
		dst := container + ":" + stageDir + "/" + stem + ".mac"
		if res, err := LocalRunner(ctx, []string{"docker", "cp", local, dst}, ""); err != nil {
			return err
		} else if res.ExitCode != 0 {
			return fmt.Errorf("engine: docker cp %s: %s", dst, strings.TrimSpace(res.Stderr))
		}
		loads = append(loads, fmt.Sprintf(`do $SYSTEM.OBJ.Load("%s/%s.mac","ck")`, stageDir, stem))
	}

	// Load everything in one pass. Individual routines that don't compile on
	// IRIS (e.g. unrelated deps with YDB-specific syntax) are not fatal — a
	// genuinely-missing routine surfaces at run time as <NOROUTINE>, which the
	// suite verdict reflects. Only a hard transport failure aborts staging.
	if _, err := eng.RunScript(ctx, strings.Join(loads, "\n")+"\nhalt\n"); err != nil {
		return err
	}
	return nil
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func shJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shQuote(a)
	}
	return strings.Join(q, " ")
}
