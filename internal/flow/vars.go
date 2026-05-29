package flow

import (
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// Per-command variable effects (the Python tool's flow/vars.py). This is the
// shared substrate for the path-sensitive local-variable rules: reaching-defs /
// definite-assignment (M-MOD-024) and taint (M-MOD-036). Globals (^X) are
// deliberately not tracked — the consuming rules target local scope only.

var (
	setLikeKW = map[string]bool{
		"S": true, "SET": true,
		"M": true, "MERGE": true,
		"R": true, "READ": true,
		"F": true, "FOR": true,
	}
	killKW = map[string]bool{"K": true, "KILL": true}
	callKW = map[string]bool{"D": true, "DO": true, "J": true, "JOB": true, "G": true, "GOTO": true}
	// $G/$D inspect a local without erroring on undefined — their first
	// argument is a defensive read, so its variable name is suppressed to avoid
	// M-MOD-024 false positives. Subscripts and later args read normally.
	defensiveIntrinsics = map[string]bool{"$G": true, "$GET": true, "$D": true, "$DATA": true}
)

// VarUse is a single read of a local variable, anchored at its AST node.
// Positions are 1-based.
type VarUse struct {
	Name string
	Node parse.Node
	Line int
	Col  int
}

// Effects are the local-variable effects of a command (or a single argument).
// Defs/Kills are name sets; KillsAll captures the argumentless KILL/NEW
// semantics (every local in the current frame); Uses is ordered so a diagnostic
// can point at a specific read site.
type Effects struct {
	Defs     map[string]bool
	Kills    map[string]bool
	KillsAll bool
	Uses     []VarUse
}

func newEffects() Effects {
	return Effects{Defs: map[string]bool{}, Kills: map[string]bool{}}
}

func (e *Effects) merge(o Effects) {
	for k := range o.Defs {
		e.Defs[k] = true
	}
	for k := range o.Kills {
		e.Kills[k] = true
	}
	e.KillsAll = e.KillsAll || o.KillsAll
	e.Uses = append(e.Uses, o.Uses...)
}

func postcondNode(cmd parse.Node) (parse.Node, bool) {
	for i := uint32(0); i < cmd.ChildCount(); i++ {
		if c := cmd.Child(i); c.Type() == "postconditional" {
			return c, true
		}
	}
	return parse.Node{}, false
}

func hasSubscripts(localVar parse.Node) bool {
	for i := uint32(0); i < localVar.ChildCount(); i++ {
		if localVar.Child(i).Type() == "subscripts" {
			return true
		}
	}
	return false
}

func isDefensiveIntrinsic(fnCall parse.Node, src []byte) bool {
	if fnCall.ChildCount() == 0 {
		return false
	}
	// The keyword leads a well-formed intrinsic call; anything else is not defensive.
	c := fnCall.Child(0)
	if c.Type() != "intrinsic_function_keyword" {
		return false
	}
	return defensiveIntrinsics[strings.ToUpper(string(textOf(c, src)))]
}

// defensiveCallChildren yields the children of a defensive function_call, but
// for the first `variable` child yields only its `subscripts` grandchild — so
// the tested variable's own name is skipped while subscript expressions are
// still walked as reads.
func defensiveCallChildren(fnCall parse.Node) []parse.Node {
	var out []parse.Node
	seenFirstVar := false
	for i := uint32(0); i < fnCall.ChildCount(); i++ {
		c := fnCall.Child(i)
		if !seenFirstVar && c.Type() == "variable" {
			seenFirstVar = true
			for j := uint32(0); j < c.ChildCount(); j++ {
				lv := c.Child(j)
				if lv.Type() != "local_variable" {
					continue
				}
				for k := uint32(0); k < lv.ChildCount(); k++ {
					if sub := lv.Child(k); sub.Type() == "subscripts" {
						out = append(out, sub)
						break
					}
				}
				break
			}
			continue
		}
		out = append(out, c)
	}
	return out
}

func makeUse(localVar parse.Node, src []byte) VarUse {
	sp := localVar.StartPoint()
	return VarUse{
		Name: identifierText(localVar, src),
		Node: localVar,
		Line: int(sp.Row) + 1,
		Col:  int(sp.Column) + 1,
	}
}

// walkLocalVars returns every local_variable node in node's subtree, in source
// order. It skips global_variable subtrees, applies defensive-intrinsic
// suppression, and for a local_variable recurses only into its subscripts (the
// identifier is the variable's name, not a separate use).
func walkLocalVars(node parse.Node, src []byte) []parse.Node {
	var out []parse.Node
	var rec func(n parse.Node)
	rec = func(n parse.Node) {
		switch {
		case n.Type() == "global_variable":
			return
		case n.Type() == "function_call" && isDefensiveIntrinsic(n, src):
			for _, c := range defensiveCallChildren(n) {
				rec(c)
			}
			return
		case n.Type() == "local_variable":
			out = append(out, n)
			for i := uint32(0); i < n.ChildCount(); i++ {
				if c := n.Child(i); c.Type() == "subscripts" {
					rec(c)
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

// walkSetLikeArg walks a SET / MERGE / READ / FOR argument: the first
// local_variable in source order is the LHS/target (a DEF); subsequent
// local_variable nodes are USES. by_reference nodes (e.g. inside $$F(.X) on the
// RHS) are DEFs — the callee may initialize the variable in the caller frame.
func walkSetLikeArg(arg parse.Node, src []byte, out *Effects) {
	targetAssigned := false
	var visit func(n parse.Node)
	visit = func(n parse.Node) {
		switch {
		case n.Type() == "global_variable":
			return
		case n.Type() == "function_call" && isDefensiveIntrinsic(n, src):
			for _, c := range defensiveCallChildren(n) {
				visit(c)
			}
			return
		case n.Type() == "by_reference":
			if name := identifierText(n, src); name != "" {
				out.Defs[name] = true
			}
			return
		case n.Type() == "local_variable":
			if !targetAssigned {
				targetAssigned = true
				out.Defs[identifierText(n, src)] = true
			} else {
				out.Uses = append(out.Uses, makeUse(n, src))
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
}

// walkGenericArg walks a generic-command argument (W, Q, etc.): every
// local_variable is a USE; a by_reference contributes a DEF for safety.
func walkGenericArg(arg parse.Node, src []byte, out *Effects) {
	var visit func(n parse.Node)
	visit = func(n parse.Node) {
		switch {
		case n.Type() == "global_variable":
			return
		case n.Type() == "function_call" && isDefensiveIntrinsic(n, src):
			for _, c := range defensiveCallChildren(n) {
				visit(c)
			}
			return
		case n.Type() == "by_reference":
			if name := identifierText(n, src); name != "" {
				out.Defs[name] = true
			}
			return
		case n.Type() == "local_variable":
			out.Uses = append(out.Uses, makeUse(n, src))
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
}

// callArgSubscripts finds the subscripts node holding a DO/JOB/GOTO call's
// parameter list. The target is wrapped either as variable > local_variable
// (D LBL(X)) or as entry_reference (D LBL^ROUTINE(X)). Returns false when the
// call has no parameter list (D LBL / D ^ROUTINE).
func callArgSubscripts(arg parse.Node) (parse.Node, bool) {
	for i := uint32(0); i < arg.ChildCount(); i++ {
		c := arg.Child(i)
		switch c.Type() {
		case "variable":
			for j := uint32(0); j < c.ChildCount(); j++ {
				lv := c.Child(j)
				if lv.Type() != "local_variable" {
					continue
				}
				for k := uint32(0); k < lv.ChildCount(); k++ {
					if sub := lv.Child(k); sub.Type() == "subscripts" {
						return sub, true
					}
				}
			}
			return parse.Node{}, false
		case "entry_reference":
			for j := uint32(0); j < c.ChildCount(); j++ {
				if sub := c.Child(j); sub.Type() == "subscripts" {
					return sub, true
				}
			}
			return parse.Node{}, false
		}
	}
	return parse.Node{}, false
}

// walkCallArg walks a DO/JOB/GOTO argument: the call target contributes nothing
// (it is a label, not a variable); within the parameter list a by_reference
// (.X) is a DEF and any other local_variable is a USE.
func walkCallArg(arg parse.Node, src []byte, out *Effects) {
	subs, ok := callArgSubscripts(arg)
	if !ok {
		return
	}
	var visit func(n parse.Node)
	visit = func(n parse.Node) {
		switch {
		case n.Type() == "global_variable":
			return
		case n.Type() == "function_call" && isDefensiveIntrinsic(n, src):
			for _, c := range defensiveCallChildren(n) {
				visit(c)
			}
			return
		case n.Type() == "by_reference":
			if name := identifierText(n, src); name != "" {
				out.Defs[name] = true
			}
			return
		case n.Type() == "local_variable":
			out.Uses = append(out.Uses, makeUse(n, src))
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
	visit(subs)
}

// effectsOfArgument returns the effects of evaluating one argument of a command.
// keyword is the uppercased command keyword. For DO/JOB/GOTO, by-reference args
// (.X) contribute defs and by-value args contribute uses; the call target's
// identifier is tracked as neither.
func effectsOfArgument(arg parse.Node, src []byte, keyword string) Effects {
	out := newEffects()
	switch {
	case setLikeKW[keyword]:
		walkSetLikeArg(arg, src, &out)
	case killKW[keyword] || newKW[keyword]:
		lvars := walkLocalVars(arg, src)
		if len(lvars) > 0 {
			target := lvars[0]
			if hasSubscripts(target) {
				// Partial kill — the base variable stays defined; subscripts are reads.
				for _, lv := range lvars[1:] {
					out.Uses = append(out.Uses, makeUse(lv, src))
				}
			} else {
				out.Kills[identifierText(target, src)] = true
			}
		}
	case callKW[keyword]:
		walkCallArg(arg, src, &out)
	default:
		walkGenericArg(arg, src, &out)
	}
	return out
}

// usesInSubtree returns every local-variable USE inside node, in source order.
func usesInSubtree(node parse.Node, src []byte) []VarUse {
	lvars := walkLocalVars(node, src)
	out := make([]VarUse, 0, len(lvars))
	for _, lv := range lvars {
		out = append(out, makeUse(lv, src))
	}
	return out
}

// effects aggregates the local-variable effects of a single command node across
// its postconditional (uses) and every argument. For S A=1,B=A this rolls A
// into both defs and uses — rules that need running per-argument state should
// walk argumentNodes and call effectsOfArgument directly.
func effects(cmd parse.Node, src []byte) Effects {
	out := newEffects()
	kw := commandKeyword(cmd, src)

	if pc, ok := postcondNode(cmd); ok {
		out.Uses = append(out.Uses, usesInSubtree(pc, src)...)
	}
	if kw == "" {
		return out
	}

	args := argumentNodes(cmd)
	if len(args) == 0 && (killKW[kw] || newKW[kw]) {
		out.KillsAll = true
		return out
	}
	for _, arg := range args {
		out.merge(effectsOfArgument(arg, src, kw))
	}
	return out
}

// FormalParams maps each label's start row (0-based, matching CFG.LabelRow) to
// the names of its declared formal parameters. LBL(A,B) → {row: ["A", "B"]}.
// Labels with no formals are absent. The structural walk mirrors the CFG build,
// since m-parse exposes no parent pointers.
func FormalParams(root parse.Node, src []byte) map[int][]string {
	out := map[int][]string{}
	for i := uint32(0); i < root.NamedChildCount(); i++ {
		line := root.NamedChild(i)
		if line.Type() != "line" {
			continue
		}
		labelRow := -1
		var names []string
		for j := uint32(0); j < line.ChildCount(); j++ {
			ch := line.Child(j)
			switch ch.Type() {
			case "label":
				labelRow = int(ch.StartPoint().Row)
			case "formals":
				for k := uint32(0); k < ch.ChildCount(); k++ {
					if id := ch.Child(k); id.Type() == "identifier" {
						names = append(names, string(textOf(id, src)))
					}
				}
			}
		}
		if labelRow >= 0 && len(names) > 0 {
			out[labelRow] = names
		}
	}
	return out
}
