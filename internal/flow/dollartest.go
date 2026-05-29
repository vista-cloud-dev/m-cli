package flow

import (
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// $TEST-setting commands (conservative: every command that *can* write $TEST,
// timeout-bearing or not — IF always writes it, the timeout forms of
// OPEN/LOCK/READ/JOB write it on timeout). ELSE/FOR read $TEST but don't set it.
var testSetterKW = map[string]bool{
	"I": true, "IF": true,
	"O": true, "OPEN": true,
	"L": true, "LOCK": true,
	"R": true, "READ": true,
	"J": true, "JOB": true,
}

var dollarTestNames = map[string]bool{"$TEST": true, "$T": true}

// StaleTestRead is a read of $TEST ($T) at a point where no $TEST-setting
// command is guaranteed to have run on every path from the label entry — the
// value may be left over from a much earlier command (M-MOD-017). Positions are
// 1-based; Col/EndCol span the special variable.
type StaleTestRead struct {
	Label  string
	Line   int
	Col    int
	EndCol int
}

// StaleTestReads returns the stale $TEST reads in the label, one per source line
// (consecutive reads collapse). It runs the freshness MUST-analysis, then
// reports $TEST/$T reads in blocks whose entry is not fresh.
func StaleTestReads(cfg CFG, src []byte) []StaleTestRead {
	fresh := analyzeTestFreshness(cfg, src)
	var out []StaleTestRead
	reportedLines := map[int]bool{}
	for _, b := range cfg.Blocks {
		if b.Kind != "command" || !b.HasCmd || fresh[b.ID] {
			continue
		}
		for _, n := range testReadNodes(b.Command, src) {
			sp := n.StartPoint()
			line := int(sp.Row) + 1
			if reportedLines[line] {
				continue
			}
			reportedLines[line] = true
			ep := n.EndPoint()
			out = append(out, StaleTestRead{
				Label: cfg.LabelName, Line: line,
				Col: int(sp.Column) + 1, EndCol: int(ep.Column) + 1,
			})
		}
	}
	return out
}

// analyzeTestFreshness is a forward MUST-analysis (AND meet): in[B] is true iff
// a $TEST-setter has run on EVERY path from the label entry to B. Entry starts
// not-fresh; other blocks start at the lattice top (true) and refine downward.
func analyzeTestFreshness(cfg CFG, src []byte) map[int]bool {
	preds := make(map[int][]predEdge, len(cfg.Blocks))
	for _, b := range cfg.Blocks {
		for k, s := range b.Succ {
			preds[s] = append(preds[s], predEdge{id: b.ID, kind: b.Edges[k]})
		}
	}

	state := make(map[int]bool, len(cfg.Blocks))
	for _, b := range cfg.Blocks {
		state[b.ID] = true
	}
	state[0] = false
	computed := map[int]bool{0: true}

	work := append([]int(nil), cfg.Blocks[0].Succ...)
	queued := make(map[int]bool, len(cfg.Blocks))
	for _, s := range work {
		queued[s] = true
	}

	for len(work) > 0 {
		bid := work[0]
		work = work[1:]
		queued[bid] = false
		if bid == 0 {
			continue
		}
		seen := false
		newState := true
		for _, p := range preds[bid] {
			if !computed[p.id] {
				continue
			}
			seen = true
			if !testFreshOutForEdge(state[p.id], cfg.Blocks[p.id], p.kind, src) {
				newState = false
			}
		}
		if !seen {
			continue
		}
		if computed[bid] && newState == state[bid] {
			continue
		}
		state[bid] = newState
		computed[bid] = true
		for _, s := range cfg.Blocks[bid].Succ {
			if !queued[s] {
				work = append(work, s)
				queued[s] = true
			}
		}
	}

	for _, b := range cfg.Blocks {
		if !computed[b.ID] {
			state[b.ID] = false
		}
	}
	return state
}

// testFreshOutForEdge: a setter makes $TEST fresh on every edge it can leave by
// EXCEPT a postconditional "skip" (the command didn't run). Unlike the other
// passes, the IF's "if-skip" edge still applies the setter — the IF ran to
// evaluate its condition, which sets $TEST, even though the line tail was skipped.
func testFreshOutForEdge(in bool, b Block, edge string, src []byte) bool {
	if !b.HasCmd || edge == "skip" {
		return in
	}
	if testSetterKW[commandKeyword(b.Command, src)] {
		return true
	}
	return in
}

// testReadNodes returns every $TEST / $T special_variable node in cmd's subtree.
func testReadNodes(cmd parse.Node, src []byte) []parse.Node {
	var out []parse.Node
	var rec func(n parse.Node)
	rec = func(n parse.Node) {
		if n.Type() == "special_variable" {
			if dollarTestNames[strings.ToUpper(string(textOf(n, src)))] {
				out = append(out, n)
			}
			return
		}
		for i := uint32(0); i < n.ChildCount(); i++ {
			rec(n.Child(i))
		}
	}
	rec(cmd)
	return out
}
