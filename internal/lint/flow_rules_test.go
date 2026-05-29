package lint_test

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/lint"
)

// M-MOD-024: a read of a local never assigned on any prior path is flagged as
// an error, anchored at the read site. It is in modern (not default).
func TestReadOfUndefinedFlagged(t *testing.T) {
	l := newLinter(t, lint.Profile("modern"))
	src := []byte("EN ;\n write X\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings %+v, want 1 (M-MOD-024)", len(findings), findings)
	}
	f := findings[0]
	if f.Rule != "M-MOD-024" || f.Severity != lint.Error {
		t.Errorf("got %+v, want rule M-MOD-024 severity error", f)
	}
	if f.Line != 2 { // anchored at the read site, not the label header
		t.Errorf("anchor line = %d, want 2 (the read site)", f.Line)
	}
}

// A local defined before use produces no M-MOD-024 finding.
func TestReadOfUndefinedDefinedClean(t *testing.T) {
	l := newLinter(t, lint.Profile("modern"))
	src := []byte("EN ;\n set X=1\n write X\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings %+v, want 0", len(findings), findings)
	}
}

// VistA Kernel auto-defined locals (U, DUZ, IO, ...) are suppressed even though
// the static analysis cannot see Kernel's init.
func TestReadOfUndefinedKernelAllowlisted(t *testing.T) {
	l := newLinter(t, lint.Profile("modern"))
	// Both U and DATA are genuine reads; U is Kernel-allowlisted and skipped,
	// DATA is a real undefined read and must still fire.
	src := []byte("EN ;\n write U,DATA\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings %+v, want 1 (DATA only; U suppressed)", len(findings), findings)
	}
	if findings[0].Rule != "M-MOD-024" {
		t.Errorf("got rule %q, want M-MOD-024", findings[0].Rule)
	}
}

// One finding per (label, variable): a long run of the same undefined read
// collapses to a single finding.
func TestReadOfUndefinedDedup(t *testing.T) {
	l := newLinter(t, lint.Profile("modern"))
	src := []byte("EN ;\n write X,X,X\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings %+v, want 1 (deduped on X)", len(findings), findings)
	}
}

// M-MOD-024 is FP-prone — excluded from the curated default profile, present in
// modern/pedantic/all.
func TestReadOfUndefinedNotInDefault(t *testing.T) {
	src := []byte("EN ;\n write X\n quit\n")
	if f, err := newLinter(t, lint.Profile("default")).Lint(context.Background(), src); err != nil || len(f) != 0 {
		t.Fatalf("default: got %d findings (err %v), want 0 (M-MOD-024 excluded)", len(f), err)
	}
	if f, err := newLinter(t, lint.Profile("pedantic")).Lint(context.Background(), src); err != nil || len(f) != 1 {
		t.Fatalf("pedantic: got %d findings (err %v), want 1", len(f), err)
	}
}

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

// M-MOD-027: SET $ETRAP with no preceding NEW $ETRAP is flagged at the SET site.
func TestEtrapLeakFlagged(t *testing.T) {
	l := newLinter(t, lint.Profile("default"))
	src := []byte("E ;\n set $etrap=\"d ^err\"\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("got %d findings %+v, want 1 (M-MOD-027)", len(findings), findings)
	}
	f := findings[0]
	if f.Rule != "M-MOD-027" || f.Severity != lint.Error {
		t.Errorf("got %+v, want rule M-MOD-027 severity error", f)
	}
	if f.Line != 2 { // anchored at the SET command, not the label header
		t.Errorf("anchor line = %d, want 2 (the SET site)", f.Line)
	}
}

// NEW $ETRAP before the SET clears M-MOD-027.
func TestEtrapGuardedClean(t *testing.T) {
	l := newLinter(t, lint.Profile("default"))
	src := []byte("E ;\n new $etrap\n set $etrap=\"d ^err\"\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Errorf("got %d findings %+v, want 0", len(findings), findings)
	}
}
