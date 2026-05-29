package flow

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// definiteAtLine builds the first label's CFG, runs the definite-assignment
// analysis, and returns the in-set of the first command block on the given
// 1-based source line.
func definiteAtLine(t *testing.T, src string, formals []string, line int) map[string]bool {
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
	if len(cfgs) == 0 {
		t.Fatalf("no CFGs for %q", src)
	}
	in := DefinitelyDefined(cfgs[0], []byte(src), formals)
	for _, b := range cfgs[0].Blocks {
		if b.Kind == "command" && b.Line == line {
			return in[b.ID]
		}
	}
	t.Fatalf("no command block on line %d of %q", line, src)
	return nil
}

func TestReachingStraightLine(t *testing.T) {
	// set X=1 ; write X — X is definitely defined when the write runs.
	in := definiteAtLine(t, "EN ;\n set X=1\n write X\n quit\n", nil, 3)
	if !in["X"] {
		t.Errorf("X not definitely defined at the write; in = %v", sortedKeys(in))
	}
}

func TestReachingConditionalDefine(t *testing.T) {
	// i A s X=1 ; write X — the IF-false path skips the SET, so X is NOT
	// definitely defined at the write (intersection meet).
	in := definiteAtLine(t, "EN ;\n i A s X=1\n write X\n quit\n", nil, 3)
	if in["X"] {
		t.Errorf("X should not be definitely defined at the write (skipped on IF-false path); in = %v", sortedKeys(in))
	}
}

func TestReachingFormalsAtEntry(t *testing.T) {
	// A formal parameter is definitely defined throughout the label.
	in := definiteAtLine(t, "LBL(A) ;\n write A\n quit\n", []string{"A"}, 2)
	if !in["A"] {
		t.Errorf("formal A not definitely defined at the write; in = %v", sortedKeys(in))
	}
}

func TestReachingKillRemoves(t *testing.T) {
	// set X=1 ; kill X ; write X — the KILL undefs X again.
	in := definiteAtLine(t, "EN ;\n set X=1\n kill X\n write X\n quit\n", nil, 4)
	if in["X"] {
		t.Errorf("X should not be definitely defined after KILL X; in = %v", sortedKeys(in))
	}
}

func TestReachingKillAllRemoves(t *testing.T) {
	// set X=1 ; kill ; write X — argumentless KILL clears everything.
	in := definiteAtLine(t, "EN ;\n set X=1\n kill\n write X\n quit\n", nil, 4)
	if in["X"] {
		t.Errorf("X should not be definitely defined after argumentless KILL; in = %v", sortedKeys(in))
	}
}
