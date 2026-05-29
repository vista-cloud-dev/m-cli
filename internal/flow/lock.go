package flow

import (
	"sort"
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

var lockKW = map[string]bool{"L": true, "LOCK": true}

// HeldAtExit returns the set of lock names still held when control reaches the
// label's exit on any path — the LOCK-leak signal (M-MOD-025). The result is
// sorted for determinism.
func HeldAtExit(cfg CFG, src []byte) []string {
	in := analyzeLocks(cfg, src)
	held := in[cfg.ExitID()]
	out := make([]string, 0, len(held))
	for k := range held {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type predEdge struct {
	id   int
	kind string
}

// analyzeLocks runs a forward, union-meet dataflow over cfg: in[b] is the set of
// lock names held on entry to b, computed as the union of each predecessor's
// out-set along its edge. The entry block is always clean. A LOCK command on a
// command block transforms the set (see applyCommand); skip/if-skip edges carry
// the in-set through unchanged.
func analyzeLocks(cfg CFG, src []byte) map[int]map[string]bool {
	preds := make(map[int][]predEdge, len(cfg.Blocks))
	for _, b := range cfg.Blocks {
		for k, s := range b.Succ {
			preds[s] = append(preds[s], predEdge{id: b.ID, kind: b.Edges[k]})
		}
	}

	in := make(map[int]map[string]bool, len(cfg.Blocks))
	work := make([]int, 0, len(cfg.Blocks))
	queued := make(map[int]bool, len(cfg.Blocks))
	for _, b := range cfg.Blocks {
		in[b.ID] = map[string]bool{}
		work = append(work, b.ID)
		queued[b.ID] = true
	}

	for len(work) > 0 {
		bid := work[0]
		work = work[1:]
		queued[bid] = false

		newIn := map[string]bool{}
		if bid != 0 { // entry is always clean
			for _, p := range preds[bid] {
				for k := range outForEdge(in[p.id], cfg.Blocks[p.id], p.kind, src) {
					newIn[k] = true
				}
			}
		}
		if setEqual(newIn, in[bid]) {
			continue
		}
		in[bid] = newIn
		for _, s := range cfg.Blocks[bid].Succ {
			if !queued[s] {
				work = append(work, s)
				queued[s] = true
			}
		}
	}
	return in
}

// outForEdge is the lock set leaving block b along an edge. skip / if-skip edges
// (a command not executed, or an IF whose body is bypassed) carry the in-set
// through unchanged; every other edge applies the block's command transfer.
func outForEdge(in map[string]bool, b Block, edge string, src []byte) map[string]bool {
	if !b.HasCmd || edge == "skip" || edge == "if-skip" {
		return in
	}
	return applyCommand(in, b.Command, src)
}

// applyCommand is the LOCK transfer function. A non-LOCK command is transparent.
// Argumentless LOCK releases everything. Per argument: a "+name" incremental
// lock adds, a "-name" releases, and a plain "name" lock first releases all then
// holds exactly the listed names (LOCK's replace-all semantics).
func applyCommand(in map[string]bool, cmd parse.Node, src []byte) map[string]bool {
	if !lockKW[commandKeyword(cmd, src)] {
		return in
	}
	args := argumentNodes(cmd)
	if len(args) == 0 {
		return map[string]bool{}
	}
	// Detect whether any argument is a plain (non +/-) lock: a plain lock means
	// LOCK first drops every prior lock, then takes the listed names.
	hasPlain := false
	for _, a := range args {
		if pol, name := lockArg(a, src); name != "" && pol == "" {
			hasPlain = true
			break
		}
	}
	held := map[string]bool{}
	if !hasPlain {
		held = copySet(in)
	}
	for _, a := range args {
		pol, name := lockArg(a, src)
		if name == "" {
			continue
		}
		switch pol {
		case "-":
			delete(held, name)
		default: // "+" incremental or "" plain — both add the name
			held[name] = true
		}
	}
	return held
}

// argumentNodes returns the `argument` children of a command's argument_list.
func argumentNodes(cmd parse.Node) []parse.Node {
	var out []parse.Node
	for i := uint32(0); i < cmd.ChildCount(); i++ {
		al := cmd.Child(i)
		if al.Type() != "argument_list" {
			continue
		}
		for j := uint32(0); j < al.ChildCount(); j++ {
			if a := al.Child(j); a.Type() == "argument" {
				out = append(out, a)
			}
		}
	}
	return out
}

// lockArg decodes one LOCK argument into (polarity, name): polarity is "+"
// (incremental), "-" (release), or "" (plain). name is the lock target: a local
// variable's identifier, "^"+identifier for a global, or "@" for an indirection
// (which we cannot resolve statically — treated as a single opaque name).
func lockArg(arg parse.Node, src []byte) (string, string) {
	for i := uint32(0); i < arg.ChildCount(); i++ {
		ch := arg.Child(i)
		switch ch.Type() {
		case "unary_expression":
			pol := ""
			for j := uint32(0); j < ch.ChildCount(); j++ {
				gc := ch.Child(j)
				switch gc.Type() {
				case "operator":
					op := strings.TrimSpace(string(textOf(gc, src)))
					if op == "+" || op == "-" {
						pol = op
					}
				case "variable":
					return pol, variableName(gc, src)
				case "indirection":
					return pol, "@"
				}
			}
			return pol, ""
		case "variable":
			return "", variableName(ch, src)
		case "indirection":
			return "", "@"
		}
	}
	return "", ""
}

// variableName renders a `variable` node as a lock name: a local variable is its
// identifier; a global variable is "^"+identifier.
func variableName(v parse.Node, src []byte) string {
	for i := uint32(0); i < v.ChildCount(); i++ {
		ch := v.Child(i)
		switch ch.Type() {
		case "local_variable":
			return identifierText(ch, src)
		case "global_variable":
			return "^" + identifierText(ch, src)
		}
	}
	return ""
}

func identifierText(n parse.Node, src []byte) string {
	for i := uint32(0); i < n.ChildCount(); i++ {
		if ch := n.Child(i); ch.Type() == "identifier" {
			return string(textOf(ch, src))
		}
	}
	return ""
}

func textOf(n parse.Node, src []byte) []byte {
	s, e := n.StartByte(), n.EndByte()
	if s > e || int(e) > len(src) {
		return nil
	}
	return src[s:e]
}

func copySet(s map[string]bool) map[string]bool {
	out := make(map[string]bool, len(s))
	for k := range s {
		out[k] = true
	}
	return out
}

func setEqual(a, b map[string]bool) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}
