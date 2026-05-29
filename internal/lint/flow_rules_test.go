package lint_test

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/lint"
)

// M-MOD-025: a LOCK never released before the label exits is flagged as an
// error, anchored at the label header.
func TestLockLeakFlagged(t *testing.T) {
	l := newLinter(t, lint.Profile("default"))
	src := []byte("LEAK ;\n lock ^FOO\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings %+v, want 1 (M-MOD-025)", len(findings), findings)
	}
	f := findings[0]
	if f.Rule != "M-MOD-025" || f.Severity != lint.Error {
		t.Errorf("got %+v, want rule M-MOD-025 severity error", f)
	}
	if f.Line != 1 || f.Col != 1 {
		t.Errorf("anchor = %d:%d, want 1:1 (label header)", f.Line, f.Col)
	}
}

// A balanced LOCK (released before exit) produces no finding.
func TestLockReleasedClean(t *testing.T) {
	l := newLinter(t, lint.Profile("default"))
	src := []byte("SAFE ;\n lock ^FOO\n lock\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings %+v, want 0", len(findings), findings)
	}
}

// The leak rule is part of the modern/default set; pedantic-only profile excludes
// it (it carries only the "modern" tag).
func TestLockLeakInModernNotPedantic(t *testing.T) {
	src := []byte("LEAK ;\n lock ^FOO\n quit\n")

	if f, err := newLinter(t, lint.Profile("modern")).Lint(context.Background(), src); err != nil || len(f) != 1 {
		t.Fatalf("modern: got %d findings (err %v), want 1", len(f), err)
	}
	if f, err := newLinter(t, lint.Profile("pedantic")).Lint(context.Background(), src); err != nil || len(f) != 0 {
		t.Fatalf("pedantic: got %d findings (err %v), want 0", len(f), err)
	}
}

// M-MOD-026: a TSTART with no matching TCOMMIT/TROLLBACK before exit is flagged.
func TestTransactionLeakFlagged(t *testing.T) {
	l := newLinter(t, lint.Profile("default"))
	src := []byte("TX ;\n tstart\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings %+v, want 1 (M-MOD-026)", len(findings), findings)
	}
	f := findings[0]
	if f.Rule != "M-MOD-026" || f.Severity != lint.Error {
		t.Errorf("got %+v, want rule M-MOD-026 severity error", f)
	}
	if f.Line != 1 || f.Col != 1 {
		t.Errorf("anchor = %d:%d, want 1:1 (label header)", f.Line, f.Col)
	}
}

// A balanced transaction (TSTART…TCOMMIT before exit) produces no finding.
func TestTransactionBalancedClean(t *testing.T) {
	l := newLinter(t, lint.Profile("default"))
	src := []byte("TX ;\n tstart\n tcommit\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings %+v, want 0", len(findings), findings)
	}
}
