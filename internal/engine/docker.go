package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// DockerRunner is a Runner transport that runs argv inside a running container
// via `docker exec -i <container> bash -lc …` — `bash -lc` loads the engine's
// shell env (e.g. YottaDB's $ydb_dist/$ydb_routines). When stageDir is
// non-empty it is prepended to $ydb_routines so routines staged there resolve
// and auto-compile (the m-test-engine bind-mounts $HOME/m-work → /m-work, but
// /m-work is not on the default $ydb_routines).
func DockerRunner(container, stageDir string) Runner {
	return func(ctx context.Context, argv []string, stdin string) (Result, error) {
		inner := shJoin(argv)
		if stageDir != "" {
			inner = `export ydb_routines="` + stageDir + ` $ydb_routines"; ` + inner
		}
		dargv := []string{"docker", "exec", "-i", container, "bash", "-lc", inner}
		return LocalRunner(ctx, dargv, stdin)
	}
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

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func shJoin(argv []string) string {
	q := make([]string, len(argv))
	for i, a := range argv {
		q[i] = shQuote(a)
	}
	return strings.Join(q, " ")
}
