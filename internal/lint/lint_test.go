package lint_test

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/lint"
	"github.com/vista-cloud-dev/m-parse/parse"
)

func newLinter(t *testing.T, rules []lint.Rule) *lint.Linter {
	t.Helper()
	p, err := parse.New(context.Background())
	if err != nil {
		t.Fatalf("parse.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	l, err := lint.NewLinter(p, rules)
	if err != nil {
		t.Fatalf("NewLinter: %v", err)
	}
	t.Cleanup(l.Close)
	return l
}

// M-MOD-037: a subscripted by-reference argument is flagged.
func TestByRefSubscriptFlagged(t *testing.T) {
	l := newLinter(t, lint.Profile("default"))
	src := []byte("EN ;\n do work(.x(1))\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings %+v, want 1 (M-MOD-037)", len(findings), findings)
	}
	if findings[0].Rule != "M-MOD-037" || findings[0].Severity != lint.Error {
		t.Errorf("got %+v, want rule M-MOD-037 severity error", findings[0])
	}
	if findings[0].Line != 2 {
		t.Errorf("finding line = %d, want 2", findings[0].Line)
	}
}

// A whole-local by-reference (do work(.x)) is valid and must NOT be flagged.
func TestWholeLocalByRefClean(t *testing.T) {
	l := newLinter(t, lint.Profile("default"))
	findings, err := l.Lint(context.Background(), []byte("EN ;\n do work(.x)\n quit\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings %+v, want 0", len(findings), findings)
	}
}

// The default profile excludes the style rule; modern includes it.
func TestProfilesSelectRules(t *testing.T) {
	src := []byte("EN ;\n s x=1 w x\n")

	def := newLinter(t, lint.Profile("default"))
	if f, err := def.Lint(context.Background(), src); err != nil || len(f) != 0 {
		t.Fatalf("default profile: got %d findings (err %v), want 0 — style rule must be excluded", len(f), err)
	}

	mod := newLinter(t, lint.Profile("modern"))
	f, err := mod.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	// s and w are abbreviated → 2 style findings.
	if len(f) != 2 {
		t.Fatalf("modern profile: got %d findings %+v, want 2 (M-STY-001 ×2)", len(f), f)
	}
	for _, fd := range f {
		if fd.Rule != "M-STY-001" || fd.Severity != lint.Style {
			t.Errorf("got %+v, want M-STY-001/style", fd)
		}
	}
}

func TestAllProfileBundlesBoth(t *testing.T) {
	l := newLinter(t, lint.Profile("all"))
	src := []byte("EN ;\n d work(.x(1)) s y=2\n")
	f, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]int{}
	for _, fd := range f {
		rules[fd.Rule]++
	}
	if rules["M-MOD-037"] != 1 {
		t.Errorf("M-MOD-037 count = %d, want 1", rules["M-MOD-037"])
	}
	if rules["M-STY-001"] == 0 {
		t.Errorf("expected M-STY-001 findings for abbreviated d/s")
	}
}
