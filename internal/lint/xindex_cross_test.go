package lint_test

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/config"
	"github.com/vista-cloud-dev/m-cli/internal/lint"
	"github.com/vista-cloud-dev/m-cli/internal/workspace"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// buildWS parses each (routine, src) and indexes it.
func buildWS(t *testing.T, p *parse.Parser, files map[string]string) *workspace.Index {
	t.Helper()
	ws := workspace.New()
	for rtn, src := range files {
		tree, err := p.Parse(context.Background(), []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		ws.AddFile(rtn, tree.RootNode())
		tree.Close()
	}
	return ws
}

// lintWithWS lints one routine's source under a workspace-attached linter.
func lintWithWS(t *testing.T, opts lint.Options, ws *workspace.Index, routine, src string) []lint.Finding {
	t.Helper()
	p, err := parse.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	l, err := lint.NewLinter(p, lint.ProfileWith("xindex", opts))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	l.AttachWorkspace(ws)
	f, err := l.LintNamed(context.Background(), []byte(src), routine)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// Without a workspace attached, the cross-routine rules never fire.
func TestCrossRoutineSkippedWithoutWorkspace(t *testing.T) {
	l := newLinter(t, lint.Profile("xindex"))
	f, err := l.LintNamed(context.Background(), []byte("CALLER ;\n D TAG^MISSING\n Q\n"), "CALLER")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"M-XINDX-007", "M-XINDX-008", "M-XINDX-049"} {
		if countRuleID(f, id) != 0 {
			t.Errorf("%s should not fire without a workspace", id)
		}
	}
}

// M-XINDX-007 — call to a routine absent from the workspace.
func TestCrossRoutine007(t *testing.T) {
	p, _ := parse.New(context.Background())
	defer p.Close(context.Background())
	ws := buildWS(t, p, map[string]string{
		"CALLER": "CALLER ;\n D ^OTHER\n D ^GHOST\n Q\n",
		"OTHER":  "OTHER ;\n Q\n",
	})
	opts := lint.DefaultOptions()
	f := lintWithWS(t, opts, ws, "CALLER", "CALLER ;\n D ^OTHER\n D ^GHOST\n Q\n")
	// ^OTHER exists; ^GHOST does not → one 007.
	if countRuleID(f, "M-XINDX-007") != 1 {
		t.Fatalf("want 1 M-XINDX-007 (^GHOST), got %+v", f)
	}

	// Trusted-routine allowlist suppresses a known FileMan API.
	g := lintWithWS(t, opts, ws, "CALLER", "CALLER ;\n D ^GHOST\n D EN^DIC\n Q\n")
	if countRuleID(g, "M-XINDX-007") != 2 {
		t.Fatalf("strict (no trusted): ^GHOST and ^DIC both flagged, got %+v", g)
	}
	topts := lint.OptionsFromConfig(config.Config{LintVistaTrustedRoutines: []string{"default"}})
	h := lintWithWS(t, topts, ws, "CALLER", "CALLER ;\n D ^GHOST\n D EN^DIC\n Q\n")
	if countRuleID(h, "M-XINDX-007") != 1 { // DIC trusted, GHOST still flagged
		t.Fatalf("trusted=default: only ^GHOST flagged, got %+v", h)
	}
}

// M-XINDX-008 — label missing in an indexed routine.
func TestCrossRoutine008(t *testing.T) {
	p, _ := parse.New(context.Background())
	defer p.Close(context.Background())
	src := "CALLER ;\n D REAL^OTHER\n D NOPE^OTHER\n Q\n"
	ws := buildWS(t, p, map[string]string{
		"CALLER": src,
		"OTHER":  "OTHER ;\nREAL ;\n Q\n",
	})
	f := lintWithWS(t, lint.DefaultOptions(), ws, "CALLER", src)
	// REAL^OTHER resolves; NOPE^OTHER doesn't → one 008.
	if countRuleID(f, "M-XINDX-008") != 1 {
		t.Fatalf("want 1 M-XINDX-008 (NOPE^OTHER), got %+v", f)
	}
	// ^GHOST (unknown routine) is 007's job, not 008.
	if countRuleID(f, "M-XINDX-008") > 1 {
		t.Error("008 should not fire for unknown routines")
	}
}

// M-XINDX-049 — a label nothing references; entry + runtime-dispatch exemptions.
func TestCrossRoutine049(t *testing.T) {
	p, _ := parse.New(context.Background())
	defer p.Close(context.Background())
	// USED is called from OTHER; ORPHAN is never referenced; MYRTN is the entry.
	files := map[string]string{
		"MYRTN": "MYRTN ;\nUSED ;\n Q\nORPHAN ;\n Q\n",
		"OTHER": "OTHER ;\n D USED^MYRTN\n Q\n",
	}
	ws := buildWS(t, p, files)
	f := lintWithWS(t, lint.DefaultOptions(), ws, "MYRTN", files["MYRTN"])
	if countRuleID(f, "M-XINDX-049") != 1 {
		t.Fatalf("want 1 M-XINDX-049 (ORPHAN only), got %+v", f)
	}
	for _, fd := range f {
		if fd.Rule == "M-XINDX-049" && fd.Line != 4 { // ORPHAN is line 4
			t.Errorf("049 anchored at line %d, want 4 (ORPHAN)", fd.Line)
		}
	}

	// A routine using runtime label dispatch is skipped entirely.
	rt := "DYN ;\nORPHAN ;\n Q\nGO ;\n D @TAG\n Q\n"
	ws2 := buildWS(t, p, map[string]string{"DYN": rt})
	if n := countRuleID(lintWithWS(t, lint.DefaultOptions(), ws2, "DYN", rt), "M-XINDX-049"); n != 0 {
		t.Errorf("049 should skip routines with runtime label dispatch, got %d", n)
	}
}
