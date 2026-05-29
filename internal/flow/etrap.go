package flow

import (
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

var (
	newKW      = map[string]bool{"N": true, "NEW": true}
	setKW      = map[string]bool{"S": true, "SET": true}
	etrapNames = map[string]bool{"$ETRAP": true, "$ET": true}
)

// EtrapLeak is a `SET $ETRAP=...` site not guarded by a `NEW $ETRAP` on every
// path from the label entry — the new error handler escapes the label into the
// caller's stack. Positions are 1-based; Col/EndCol span the offending command.
type EtrapLeak struct {
	Label  string
	Line   int
	Col    int
	EndCol int
}

// EtrapLeaks returns every unguarded SET $ETRAP site in the label (M-MOD-027).
// It runs the protection MUST-analysis once, then reports each SET-$ETRAP block
// whose entry is not protected.
func EtrapLeaks(cfg CFG, src []byte) []EtrapLeak {
	prot := EtrapProtection(cfg, src)
	var out []EtrapLeak
	for _, b := range cfg.Blocks {
		if b.Kind != "command" || !b.HasCmd {
			continue
		}
		if !setTargetsEtrap(b.Command, src) || prot[b.ID] {
			continue
		}
		s, e := b.Command.StartPoint(), b.Command.EndPoint()
		out = append(out, EtrapLeak{
			Label: cfg.LabelName, Line: b.Line,
			Col: int(s.Column) + 1, EndCol: int(e.Column) + 1,
		})
	}
	return out
}

// EtrapProtection is a forward MUST-analysis (AND meet): in_state[B] is true iff
// `NEW $ETRAP` (or `NEW $ET`) has executed on EVERY path from the label entry to
// B. The entry starts unprotected; other blocks start at the lattice top (true,
// the AND identity) and are refined downward as predecessors propagate.
func EtrapProtection(cfg CFG, src []byte) map[int]bool {
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
			if !etrapOutForEdge(state[p.id], cfg.Blocks[p.id], p.kind, src) {
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
			state[b.ID] = false // unreachable: assume no protection
		}
	}
	return state
}

func etrapOutForEdge(in bool, b Block, edge string, src []byte) bool {
	if !b.HasCmd || edge == "skip" || edge == "if-skip" {
		return in
	}
	if isNewEtrap(b.Command, src) {
		return true
	}
	return in
}

// isNewEtrap reports whether cmd is `NEW $ETRAP` / `NEW $ET`. Argumentless NEW
// stacks locals but does not protect ISVs, so it does not qualify.
func isNewEtrap(cmd parse.Node, src []byte) bool {
	if !newKW[commandKeyword(cmd, src)] {
		return false
	}
	for _, arg := range argumentNodes(cmd) {
		if etrapNames[argSpecialVarName(arg, src)] {
			return true
		}
	}
	return false
}

// setTargetsEtrap reports whether cmd is `SET $ETRAP=...` (an assignment whose
// LHS is the $ETRAP / $ET special variable).
func setTargetsEtrap(cmd parse.Node, src []byte) bool {
	if !setKW[commandKeyword(cmd, src)] {
		return false
	}
	for _, arg := range argumentNodes(cmd) {
		for i := uint32(0); i < arg.ChildCount(); i++ {
			be := arg.Child(i)
			if be.Type() != "binary_expression" || be.ChildCount() == 0 {
				continue
			}
			if lhs := be.Child(0); lhs.Type() == "special_variable" &&
				etrapNames[strings.ToUpper(string(textOf(lhs, src)))] {
				return true
			}
		}
	}
	return false
}

// argSpecialVarName returns the uppercased special-variable name when arg
// directly references one (e.g. "$ETRAP"); "" otherwise.
func argSpecialVarName(arg parse.Node, src []byte) string {
	for i := uint32(0); i < arg.ChildCount(); i++ {
		if c := arg.Child(i); c.Type() == "special_variable" {
			return strings.ToUpper(string(textOf(c, src)))
		}
	}
	return ""
}
