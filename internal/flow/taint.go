package flow

import (
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// Taint analysis (the Python tool's flow/taint.py) — the differentiating
// security pass of the lint suite, driving M-MOD-036. A forward MAY-analysis
// over the per-label CFG: in[B] is the set of local variable names that may
// hold untrusted data on at least one path from entry to B. Meet is union (the
// conservative "may be attacker-controlled" reading), so it shares the worklist
// shape of the LOCK-leak pass (lock.go) — but note the edge rule differs: here
// only a postconditional "skip" carries the in-set through unchanged; an IF's
// "if-skip" edge still applies the command (the command was reached, its
// condition merely false).
//
// Sources: READ X taints X; the label's formal parameters are tainted at entry
// when config.FormalsTainted (public-label formals are attack surface).
// Sinks (flagged by TaintFlows): every indirection node (@expr in any context)
// and the XECUTE command's arguments. Sanitizers: the subtree of any
// intrinsic-function call whose keyword is in config.Sanitizers is treated as
// clean (its output is numeric and cannot carry injected code).

// TaintConfig configures the taint analyzer.
type TaintConfig struct {
	// FormalsTainted taints the label's formal parameters at entry. Default
	// true — public-label formals are attack surface.
	FormalsTainted bool
	// Sanitizers are uppercased intrinsic-function keywords whose output is
	// treated as clean regardless of input taint (e.g. $LENGTH returns a
	// number).
	Sanitizers map[string]bool
}

// DefaultTaintConfig taints formals and treats $L/$LENGTH/$A/$ASCII as
// sanitizers — matching the Python reference defaults.
func DefaultTaintConfig() TaintConfig {
	return TaintConfig{
		FormalsTainted: true,
		Sanitizers:     map[string]bool{"$L": true, "$LENGTH": true, "$A": true, "$ASCII": true},
	}
}

var (
	taintReadKW   = map[string]bool{"R": true, "READ": true}
	taintSetKW    = map[string]bool{"S": true, "SET": true, "M": true, "MERGE": true}
	taintCallKW   = map[string]bool{"D": true, "DO": true, "J": true, "JOB": true}
	taintXecuteKW = map[string]bool{"X": true, "XECUTE": true}
)

// intrinsicKeyword returns the uppercased keyword of a function_call node (e.g.
// "$L"), or "" if the call has no intrinsic_function_keyword child.
func intrinsicKeyword(fnCall parse.Node, src []byte) string {
	for i := uint32(0); i < fnCall.ChildCount(); i++ {
		if c := fnCall.Child(i); c.Type() == "intrinsic_function_keyword" {
			return strings.ToUpper(string(textOf(c, src)))
		}
	}
	return ""
}

// firstTaintedName returns the first local_variable name in node's subtree that
// is in tainted, walking left-to-right and skipping global_variable subtrees and
// sanitizer function_call subtrees. It recurses into a local_variable's
// subscripts so that A(X) reads X. Returns "" if no tainted name is found.
func firstTaintedName(node parse.Node, src []byte, tainted, sanitizers map[string]bool) string {
	found := ""
	var visit func(n parse.Node)
	visit = func(n parse.Node) {
		if found != "" {
			return
		}
		switch n.Type() {
		case "global_variable":
			return
		case "function_call":
			if sanitizers[intrinsicKeyword(n, src)] {
				return
			}
			for i := uint32(0); i < n.ChildCount(); i++ {
				visit(n.Child(i))
			}
			return
		case "local_variable":
			if name := identifierText(n, src); tainted[name] {
				found = name
				return
			}
			for i := uint32(0); i < n.ChildCount(); i++ {
				if c := n.Child(i); c.Type() == "subscripts" {
					visit(c)
				}
			}
			return
		}
		for i := uint32(0); i < n.ChildCount(); i++ {
			visit(n.Child(i))
		}
	}
	visit(node)
	return found
}

// setArgLHS returns the leftmost local_variable name in a SET-like argument (the
// assignment target). ok is false for a malformed arg with no local target —
// e.g. S ^G=... assigns a global, which taint does not track.
func setArgLHS(arg parse.Node, src []byte) (name string, ok bool) {
	var visit func(n parse.Node) (string, bool)
	visit = func(n parse.Node) (string, bool) {
		switch n.Type() {
		case "global_variable":
			return "", false
		case "local_variable":
			return identifierText(n, src), true
		}
		for i := uint32(0); i < n.ChildCount(); i++ {
			if r, found := visit(n.Child(i)); found {
				return r, true
			}
		}
		return "", false
	}
	return visit(arg)
}

// setArgTaint reports whether the RHS of a SET-like argument propagates taint.
// It walks the arg subtree skipping the leftmost local_variable's identifier
// (the LHS, not a read — but its subscripts ARE reads) and sanitizer calls; any
// other tainted local_variable returns true.
func setArgTaint(arg parse.Node, src []byte, tainted, sanitizers map[string]bool) bool {
	found := false
	seenFirst := false
	var visit func(n parse.Node)
	visit = func(n parse.Node) {
		if found {
			return
		}
		switch n.Type() {
		case "global_variable":
			return
		case "function_call":
			if sanitizers[intrinsicKeyword(n, src)] {
				return
			}
			for i := uint32(0); i < n.ChildCount(); i++ {
				visit(n.Child(i))
			}
			return
		case "local_variable":
			if !seenFirst {
				seenFirst = true
				for i := uint32(0); i < n.ChildCount(); i++ {
					if c := n.Child(i); c.Type() == "subscripts" {
						visit(c)
					}
				}
				return
			}
			if tainted[identifierText(n, src)] {
				found = true
				return
			}
			for i := uint32(0); i < n.ChildCount(); i++ {
				if c := n.Child(i); c.Type() == "subscripts" {
					visit(c)
				}
			}
			return
		}
		for i := uint32(0); i < n.ChildCount(); i++ {
			visit(n.Child(i))
		}
	}
	visit(arg)
	return found
}

// byReferenceNames yields every identifier inside a by_reference node anywhere
// in node's subtree. A by-reference parameter (.X in D LBL(.X) or S R=$$F(.X))
// authorizes the callee to write into the caller's variable, so MAY-analysis
// taints X conservatively.
func byReferenceNames(node parse.Node, src []byte) []string {
	var out []string
	var rec func(n parse.Node)
	rec = func(n parse.Node) {
		if n.Type() == "by_reference" {
			for i := uint32(0); i < n.ChildCount(); i++ {
				if c := n.Child(i); c.Type() == "identifier" {
					if t := string(textOf(c, src)); t != "" {
						out = append(out, t)
					}
					return
				}
			}
			return
		}
		for i := uint32(0); i < n.ChildCount(); i++ {
			rec(n.Child(i))
		}
	}
	rec(node)
	return out
}

// applyTaintCommand is the forward transfer for one command, returning the OUT
// tainted set. READ taints its defs; SET/MERGE strong-update per argument (taint
// the LHS if its RHS reads a tainted var, else untaint it) with by-reference
// args tainted first so the LHS sees the post-call state; KILL/NEW untaint their
// targets (argumentless ⇒ ∅); DO/JOB taint by-reference args. Per-argument
// running state lets S A=X,B=A propagate through both names.
func applyTaintCommand(in map[string]bool, cmd parse.Node, src []byte, config TaintConfig) map[string]bool {
	kw := commandKeyword(cmd, src)
	out := copySet(in)

	switch {
	case taintReadKW[kw]:
		for _, arg := range argumentNodes(cmd) {
			for d := range effectsOfArgument(arg, src, "R").Defs {
				out[d] = true
			}
		}
		return out

	case taintSetKW[kw]:
		for _, arg := range argumentNodes(cmd) {
			for _, name := range byReferenceNames(arg, src) {
				out[name] = true
			}
			lhs, ok := setArgLHS(arg, src)
			if !ok {
				continue
			}
			if setArgTaint(arg, src, out, config.Sanitizers) {
				out[lhs] = true
			} else {
				delete(out, lhs)
			}
		}
		return out

	case killKW[kw] || newKW[kw]:
		args := argumentNodes(cmd)
		if len(args) == 0 {
			return map[string]bool{}
		}
		for _, arg := range args {
			for k := range effectsOfArgument(arg, src, "K").Kills {
				delete(out, k)
			}
		}
		return out

	case taintCallKW[kw]:
		for _, arg := range argumentNodes(cmd) {
			for _, name := range byReferenceNames(arg, src) {
				out[name] = true
			}
		}
		return out
	}
	return out
}

// taintOutForEdge is the tainted set leaving block b along an edge. Only a
// postconditional "skip" (the command did not run) carries the in-set through
// unchanged; every other edge — including an IF's "if-skip" — applies the
// command, because the command was reached. This deliberately differs from the
// LOCK pass (lock.go), which also short-circuits "if-skip".
func taintOutForEdge(in map[string]bool, b Block, edge string, src []byte, config TaintConfig) map[string]bool {
	if !b.HasCmd || edge == "skip" {
		return in
	}
	return applyTaintCommand(in, b.Command, src, config)
}

// AnalyzeTaint runs the forward union-meet taint dataflow over cfg, returning
// {blockID: tainted-at-entry}. The entry block's IN is the label's formals when
// config.FormalsTainted; every other block's IN is the union of its
// predecessors' out-sets.
func AnalyzeTaint(cfg CFG, src []byte, formals []string, config TaintConfig) map[int]map[string]bool {
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
	if config.FormalsTainted {
		for _, f := range formals {
			in[0][f] = true
		}
	}

	for len(work) > 0 {
		bid := work[0]
		work = work[1:]
		queued[bid] = false
		if bid == 0 {
			continue // entry IN is fixed (the formals seed)
		}

		newIn := map[string]bool{}
		for _, p := range preds[bid] {
			for k := range taintOutForEdge(in[p.id], cfg.Blocks[p.id], p.kind, src, config) {
				newIn[k] = true
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

// TaintFlow is one sink reached by a tainted local variable (the raw M-MOD-036
// signal, before the lint layer dedups per (label, var)). Positions are 1-based.
type TaintFlow struct {
	Label    string
	Name     string // the first tainted variable name in the sink subtree
	SinkKind string // "indirection (@…)" | "XECUTE argument"
	Line     int
	Col      int
	EndLine  int
	EndCol   int
}

// TaintFlows returns, in source order, every indirection or XECUTE-argument sink
// in cfg whose subtree references a tainted variable, naming the first such var.
// No dedup is applied here — the lint layer collapses to one finding per
// (label, var).
func TaintFlows(cfg CFG, src []byte, formals []string, config TaintConfig) []TaintFlow {
	taintSets := AnalyzeTaint(cfg, src, formals, config)
	var out []TaintFlow
	for _, b := range cfg.Blocks {
		if b.Kind != "command" || !b.HasCmd {
			continue
		}
		inTainted := taintSets[b.ID]
		kw := commandKeyword(b.Command, src)

		// Sink 1: every indirection node anywhere in the command (D @X,
		// S @X=v, S Y=@X, S Y=A_@X — all handled uniformly).
		for _, indir := range walkIndirections(b.Command) {
			if name := firstTaintedName(indir, src, inTainted, config.Sanitizers); name != "" {
				out = append(out, taintFlowAt(cfg.LabelName, name, "indirection (@…)", indir))
			}
		}
		// Sink 2: XECUTE arguments — they execute M source.
		if taintXecuteKW[kw] {
			for _, arg := range argumentNodes(b.Command) {
				if name := firstTaintedName(arg, src, inTainted, config.Sanitizers); name != "" {
					out = append(out, taintFlowAt(cfg.LabelName, name, "XECUTE argument", arg))
				}
			}
		}
	}
	return out
}

// walkIndirections returns every indirection node in node's subtree, so an
// @expr is found wherever it appears (command head, expression, or subscript).
func walkIndirections(node parse.Node) []parse.Node {
	var out []parse.Node
	var rec func(n parse.Node)
	rec = func(n parse.Node) {
		if n.Type() == "indirection" {
			out = append(out, n)
			return
		}
		for i := uint32(0); i < n.ChildCount(); i++ {
			rec(n.Child(i))
		}
	}
	rec(node)
	return out
}

func taintFlowAt(label, name, sinkKind string, anchor parse.Node) TaintFlow {
	sp := anchor.StartPoint()
	ep := anchor.EndPoint()
	return TaintFlow{
		Label:    label,
		Name:     name,
		SinkKind: sinkKind,
		Line:     int(sp.Row) + 1,
		Col:      int(sp.Column) + 1,
		EndLine:  int(ep.Row) + 1,
		EndCol:   int(ep.Column) + 1,
	}
}
