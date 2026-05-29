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

func depthAtExit(t *testing.T, src string) map[string]int {
	t.Helper()
	root, b, done := parseRoot(t, src)
	defer done()
	out := map[string]int{}
	for _, cfg := range flow.BuildCFGs(root, b) {
		out[cfg.LabelName] = flow.DepthAtExit(cfg, b)
	}
	return out
}

func TestTransactionBalanced(t *testing.T) {
	if got := depthAtExit(t, "TX ;\n tstart\n tcommit\n quit\n")["TX"]; got != 0 {
		t.Errorf("depth at exit = %d, want 0 (TSTART then TCOMMIT)", got)
	}
}

func TestTransactionLeak(t *testing.T) {
	if got := depthAtExit(t, "TX ;\n tstart\n quit\n")["TX"]; got != 1 {
		t.Errorf("depth at exit = %d, want 1 (TSTART never committed)", got)
	}
}

func TestTransactionNestedBalanced(t *testing.T) {
	if got := depthAtExit(t, "TX ;\n ts\n ts\n tc\n tc\n q\n")["TX"]; got != 0 {
		t.Errorf("depth at exit = %d, want 0 (two TS, two TC)", got)
	}
}

func TestTransactionRollbackCounts(t *testing.T) {
	if got := depthAtExit(t, "TX ;\n tstart\n trollback\n quit\n")["TX"]; got != 0 {
		t.Errorf("depth at exit = %d, want 0 (TROLLBACK closes)", got)
	}
}

// MAY-analysis: a path that conditionally commits but can fall through with the
// transaction open reports the worst-case (open) depth. `i 0 tc` only commits
// when the IF is true; the if-skip path leaves depth 1.
func TestTransactionLeakOnOnePath(t *testing.T) {
	if got := depthAtExit(t, "TX ;\n tstart\n i 0 tc\n quit\n")["TX"]; got != 1 {
		t.Errorf("depth at exit = %d, want 1 (commit only on the IF-true path)", got)
	}
}

func etrapLeaks(t *testing.T, src string) map[string]int {
	t.Helper()
	root, b, done := parseRoot(t, src)
	defer done()
	out := map[string]int{}
	for _, cfg := range flow.BuildCFGs(root, b) {
		out[cfg.LabelName] = len(flow.EtrapLeaks(cfg, b))
	}
	return out
}

// SET $ETRAP guarded by a preceding NEW $ETRAP on the only path is clean.
func TestEtrapProtected(t *testing.T) {
	if got := etrapLeaks(t, "E ;\n new $etrap\n set $etrap=\"d ^x\"\n quit\n")["E"]; got != 0 {
		t.Errorf("etrap leaks = %d, want 0 (NEW $ETRAP precedes SET)", got)
	}
}

// SET $ETRAP with no NEW $ETRAP anywhere leaks the handler.
func TestEtrapUnprotected(t *testing.T) {
	if got := etrapLeaks(t, "E ;\n set $etrap=\"d ^x\"\n quit\n")["E"]; got != 1 {
		t.Errorf("etrap leaks = %d, want 1 (SET without NEW)", got)
	}
}

// The abbreviations NEW $ET / SET $ET are recognized too.
func TestEtrapAbbreviations(t *testing.T) {
	if got := etrapLeaks(t, "E ;\n n $et,x\n s $ET=1\n q\n")["E"]; got != 0 {
		t.Errorf("etrap leaks = %d, want 0 (NEW $ET protects SET $ET)", got)
	}
}

// MUST-analysis: a NEW on only one branch does not protect — the if-skip path
// reaches the SET unprotected, so it leaks.
func TestEtrapNewOnOnePathLeaks(t *testing.T) {
	src := "E ;\n i 1 new $etrap\n set $etrap=\"d ^x\"\n quit\n"
	if got := etrapLeaks(t, src)["E"]; got != 1 {
		t.Errorf("etrap leaks = %d, want 1 (NEW only on the IF-true path)", got)
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
