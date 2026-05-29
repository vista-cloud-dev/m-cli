package flow

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-parse/parse"
)

func undefinedNames(t *testing.T, src string, formals []string) []string {
	t.Helper()
	p, err := parse.New(context.Background())
	if err != nil {
		t.Fatalf("parse.New: %v", err)
	}
	defer func() { _ = p.Close(context.Background()) }()
	tr, err := p.Parse(context.Background(), []byte(src))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	defer tr.Close()
	cfgs := BuildCFGs(tr.RootNode(), []byte(src))
	var out []string
	for _, cfg := range cfgs {
		for _, r := range UndefinedReads(cfg, []byte(src), formals) {
			out = append(out, r.Name)
		}
	}
	return out
}

func TestUndefinedReadFlagged(t *testing.T) {
	got := undefinedNames(t, "EN ;\n write X\n quit\n", nil)
	if !eqSlice(got, []string{"X"}) {
		t.Errorf("undefined reads = %v, want [X]", got)
	}
}

func TestUndefinedReadDefinedClean(t *testing.T) {
	got := undefinedNames(t, "EN ;\n set X=1\n write X\n quit\n", nil)
	if len(got) != 0 {
		t.Errorf("undefined reads = %v, want none", got)
	}
}

func TestUndefinedReadConditionalDefine(t *testing.T) {
	// X defined only on the IF-true path → read at the write is undefined.
	// A is a formal so the IF condition read is clean.
	got := undefinedNames(t, "LBL(A) ;\n i A s X=1\n write X\n quit\n", []string{"A"})
	if !eqSlice(got, []string{"X"}) {
		t.Errorf("undefined reads = %v, want [X]", got)
	}
}

func TestUndefinedReadFormalClean(t *testing.T) {
	got := undefinedNames(t, "LBL(A) ;\n write A\n quit\n", []string{"A"})
	if len(got) != 0 {
		t.Errorf("undefined reads = %v, want none (A is a formal)", got)
	}
}

func TestUndefinedReadProtectionIdiom(t *testing.T) {
	// IF $G(X)="" SET X=default guarantees X is defined for the rest of the
	// label — the later read must not be flagged.
	got := undefinedNames(t, "EN ;\n i $g(X)=\"\" s X=1\n write X\n quit\n", nil)
	if len(got) != 0 {
		t.Errorf("undefined reads = %v, want none (protected by IF $G(X)=\"\" SET X)", got)
	}
}

func TestUndefinedReadRunningDefsWithinCommand(t *testing.T) {
	// S X=1,Y=X — Y's RHS sees X defined by the earlier argument on the same
	// command, so neither is undefined.
	got := undefinedNames(t, "EN ;\n set X=1,Y=X\n quit\n", nil)
	if len(got) != 0 {
		t.Errorf("undefined reads = %v, want none (running defs across args)", got)
	}
}
