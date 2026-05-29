package flow_test

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/flow"
	"github.com/vista-cloud-dev/m-parse/parse"
)

func parseRoot(t *testing.T, src string) (parse.Node, []byte, func()) {
	t.Helper()
	p, err := parse.New(context.Background())
	if err != nil {
		t.Fatalf("parse.New: %v", err)
	}
	tr, err := p.Parse(context.Background(), []byte(src))
	if err != nil {
		_ = p.Close(context.Background())
		t.Fatalf("Parse: %v", err)
	}
	cleanup := func() { tr.Close(); _ = p.Close(context.Background()) }
	return tr.RootNode(), []byte(src), cleanup
}

func heldAtExit(t *testing.T, src string) map[string][]string {
	t.Helper()
	root, b, done := parseRoot(t, src)
	defer done()
	out := map[string][]string{}
	for _, cfg := range flow.BuildCFGs(root, b) {
		out[cfg.LabelName] = flow.HeldAtExit(cfg, b)
	}
	return out
}

// BuildCFGs makes one CFG per label, each with an entry (block 0), one block per
// command in source order, and an exit (last block). Commands on the label's own
// line are excluded (strict body extent).
func TestBuildCFGShape(t *testing.T) {
	root, b, done := parseRoot(t, "EN ;\n lock ^A\n quit\n")
	defer done()
	cfgs := flow.BuildCFGs(root, b)
	if len(cfgs) != 1 {
		t.Fatalf("got %d CFGs, want 1", len(cfgs))
	}
	c := cfgs[0]
	if c.LabelName != "EN" {
		t.Errorf("label = %q, want EN", c.LabelName)
	}
	// entry + lock + quit + exit = 4 blocks.
	if len(c.Blocks) != 4 {
		t.Fatalf("got %d blocks %+v, want 4", len(c.Blocks), c.Blocks)
	}
	if c.Blocks[0].Kind != "entry" || c.Blocks[c.ExitID()].Kind != "exit" {
		t.Errorf("block kinds: first=%q last=%q, want entry/exit", c.Blocks[0].Kind, c.Blocks[c.ExitID()].Kind)
	}
	// entry falls to the first command.
	if len(c.Blocks[0].Succ) != 1 || c.Blocks[0].Succ[0] != 1 {
		t.Errorf("entry succ = %v, want [1]", c.Blocks[0].Succ)
	}
	// argumentless QUIT (block 2) exits the label.
	if got := c.Blocks[2].Succ; len(got) != 1 || got[0] != c.ExitID() {
		t.Errorf("quit succ = %v, want [%d] (exit)", got, c.ExitID())
	}
	if c.Blocks[2].Edges[0] != "exit" {
		t.Errorf("quit edge = %q, want exit", c.Blocks[2].Edges[0])
	}
}

func TestLockLeakDetected(t *testing.T) {
	held := heldAtExit(t, "LEAK ;\n lock ^FOO\n quit\n")
	got := held["LEAK"]
	if len(got) != 1 || got[0] != "^FOO" {
		t.Fatalf("held at exit = %v, want [^FOO]", got)
	}
}

func TestArgumentlessLockClears(t *testing.T) {
	held := heldAtExit(t, "SAFE ;\n lock ^FOO\n lock\n quit\n")
	if got := held["SAFE"]; len(got) != 0 {
		t.Errorf("held at exit = %v, want none (argumentless LOCK releases all)", got)
	}
}

func TestIncrementalLockBalanced(t *testing.T) {
	held := heldAtExit(t, "INCR ;\n lock +^FOO\n lock -^FOO\n quit\n")
	if got := held["INCR"]; len(got) != 0 {
		t.Errorf("held at exit = %v, want none (LOCK +x then LOCK -x)", got)
	}
}

func TestIncrementalLockLeak(t *testing.T) {
	held := heldAtExit(t, "INCR ;\n lock +^FOO\n quit\n")
	if got := held["INCR"]; len(got) != 1 || got[0] != "^FOO" {
		t.Errorf("held at exit = %v, want [^FOO] (LOCK +x never released)", got)
	}
}

// A plain LOCK replaces all prior locks: LOCK ^A then LOCK ^B holds only ^B.
func TestPlainLockReplacesAll(t *testing.T) {
	held := heldAtExit(t, "REPL ;\n lock ^A\n lock ^B\n quit\n")
	got := held["REPL"]
	if len(got) != 1 || got[0] != "^B" {
		t.Errorf("held at exit = %v, want [^B] (plain LOCK is replace-all)", got)
	}
}

// A LOCK taken inside a dot-block whose argumentless QUIT only exits the block
// (not the routine) still leaks if never released — exercises the dot-block QUIT
// fall-through modeling.
func TestDotBlockLockLeak(t *testing.T) {
	held := heldAtExit(t, "DB ;\n i 1 d\n . lock ^A\n . q\n q\n")
	got := held["DB"]
	if len(got) != 1 || got[0] != "^A" {
		t.Errorf("held at exit = %v, want [^A] (lock inside dot-block leaks)", got)
	}
}

// Multiple labels are analyzed independently: a leak in one does not bleed into
// the next, and a clean label reports nothing.
func TestMultiLabelIndependent(t *testing.T) {
	held := heldAtExit(t, "A ;\n lock ^X\n quit\nB ;\n lock ^Y\n lock\n quit\n")
	if got := held["A"]; len(got) != 1 || got[0] != "^X" {
		t.Errorf("A held = %v, want [^X]", got)
	}
	if got := held["B"]; len(got) != 0 {
		t.Errorf("B held = %v, want none", got)
	}
}
