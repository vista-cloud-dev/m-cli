package engine

import (
	"context"
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		name     string
		cfg      Config
		want     Kind
		explicit bool
		wantErr  bool
	}{
		{"flag ydb", Config{Flag: "ydb"}, YDB, true, false},
		{"flag iris wins over env", Config{Flag: "iris", Env: "ydb"}, IRIS, true, false},
		{"env iris", Config{Env: "iris"}, IRIS, true, false},
		{"heuristic iris on path", Config{IrisOnPath: true}, IRIS, true, false},
		{"heuristic isc dir", Config{IscInstallDir: "/isc"}, IRIS, true, false},
		{"bare default ydb, not explicit", Config{}, YDB, false, false},
		{"bad flag", Config{Flag: "mumps"}, "", true, true},
		{"bad env", Config{Env: "gtm"}, "", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k, explicit, err := Resolve(tc.cfg)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if k != tc.want || explicit != tc.explicit {
				t.Errorf("got (%q, explicit=%v), want (%q, %v)", k, explicit, tc.want, tc.explicit)
			}
		})
	}
}

// capture is a fake Runner recording the last invocation.
type capture struct {
	argv  []string
	stdin string
}

func (c *capture) run(_ context.Context, argv []string, stdin string) (Result, error) {
	c.argv = argv
	c.stdin = stdin
	return Result{ExitCode: 0}, nil
}

func TestYdbCommands(t *testing.T) {
	c := &capture{}
	e := New(YDB, Options{Runner: c.run})
	ctx := context.Background()

	if e.Kind() != YDB {
		t.Fatalf("kind = %q", e.Kind())
	}
	if err := e.EnsureLoaded(ctx, "/x/FOO.m"); err != nil {
		t.Errorf("EnsureLoaded should be a no-op on YDB: %v", err)
	}

	_, _ = e.RunRoutine(ctx, "^FOO", "a", "b")
	if got := strings.Join(c.argv, " "); got != "ydb -run ^FOO a b" {
		t.Errorf("RunRoutine argv = %q", got)
	}

	_, _ = e.RunXCmd(ctx, "set ^X=1")
	if len(c.argv) != 4 || c.argv[0] != "ydb" || c.argv[1] != "-run" || c.argv[2] != "%XCMD" || c.argv[3] != "set ^X=1" {
		t.Errorf("RunXCmd argv = %v", c.argv)
	}
}

func TestIrisCommands(t *testing.T) {
	c := &capture{}
	e := New(IRIS, Options{Runner: c.run, Instance: "VISTA", Namespace: "VISTA"})
	ctx := context.Background()

	if e.Kind() != IRIS {
		t.Fatalf("kind = %q", e.Kind())
	}

	_, _ = e.RunRoutine(ctx, "^FOO")
	if got := strings.Join(c.argv, " "); got != "iris session VISTA -U VISTA" {
		t.Errorf("RunRoutine argv = %q", got)
	}
	if c.stdin != "do ^FOO\nhalt\n" {
		t.Errorf("RunRoutine stdin = %q, want piped `do ^FOO  halt`", c.stdin)
	}

	if err := e.EnsureLoaded(ctx, "/m/DGREG.mac"); err != nil {
		t.Fatal(err)
	}
	// EnsureLoaded pipes an OBJ.Load command over stdin.
	if !strings.Contains(c.stdin, `$SYSTEM.OBJ.Load("/m/DGREG.mac","ck")`) {
		t.Errorf("EnsureLoaded stdin = %q", c.stdin)
	}
	if !strings.HasSuffix(c.stdin, "halt\n") {
		t.Errorf("piped script should end with halt: %q", c.stdin)
	}
}

func TestLocalRunnerExitCode(t *testing.T) {
	res, err := LocalRunner(context.Background(), []string{"sh", "-c", "printf hi; exit 3"}, "")
	if err != nil {
		t.Fatalf("LocalRunner err = %v", err)
	}
	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}
	if res.Stdout != "hi" {
		t.Errorf("Stdout = %q, want hi", res.Stdout)
	}
}

func TestLocalRunnerBinaryNotFound(t *testing.T) {
	if _, err := LocalRunner(context.Background(), []string{"definitely-not-a-real-binary-xyz"}, ""); err == nil {
		t.Error("expected an error when the binary cannot be run")
	}
}
