package flow

import (
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// UndefinedRead is one read of a local variable that may not be definitely
// assigned on every path from the label entry (the raw M-MOD-024 signal, before
// the lint layer applies the Kernel allowlist and per-label dedup). Positions
// are 1-based; Col/EndCol span the variable name.
type UndefinedRead struct {
	Label  string
	Name   string
	Line   int
	Col    int
	EndCol int
}

// UndefinedReads returns, in source order, every local-variable read in cfg
// that is not guaranteed defined on every prior path. It runs the
// definite-assignment analysis, tracks running defs within each command (so
// S A=1,B=A sees A defined for B's RHS), and suppresses reads protected by the
// IF $G(X)="" SET X=default idiom. No dedup or allowlist is applied here.
//
// Known limitations (faithful to the reference): GOTO targets within the
// routine are over-approximated as exits; FOR loops have no back-edge, so a
// first-iteration read may be under-reported; OPEN device-parameter syntax can
// parse as local variables and over-report on I/O code.
func UndefinedReads(cfg CFG, src []byte, formals []string) []UndefinedRead {
	inSets := DefinitelyDefined(cfg, src, formals)
	protections := testDefaultSetProtections(cfg, src)

	var out []UndefinedRead
	flag := func(u VarUse) {
		if line, ok := protections[u.Name]; ok && u.Line > line {
			return
		}
		out = append(out, UndefinedRead{
			Label: cfg.LabelName, Name: u.Name,
			Line: u.Line, Col: u.Col, EndCol: u.Col + len(u.Name),
		})
	}

	for _, b := range cfg.Blocks {
		if b.Kind != "command" || !b.HasCmd {
			continue
		}
		running := copySet(inSets[b.ID])
		kw := commandKeyword(b.Command, src)

		if pc, ok := postcondNode(b.Command); ok {
			for _, u := range usesInSubtree(pc, src) {
				if !running[u.Name] {
					flag(u)
				}
			}
		}

		for _, arg := range argumentNodes(b.Command) {
			eff := effectsOfArgument(arg, src, kw)
			for _, u := range eff.Uses {
				if !running[u.Name] {
					flag(u)
				}
			}
			for d := range eff.Defs {
				running[d] = true
			}
			if eff.KillsAll {
				running = map[string]bool{}
			} else {
				for k := range eff.Kills {
					delete(running, k)
				}
			}
		}
	}
	return out
}

// testDefaultSetProtections detects the canonical IF $G(X)="" SET X=default
// idiom (and $D variants) on a single line, returning {var: protection_line}
// where protection_line is the 1-based line of the IF. After that line X is
// guaranteed defined on every path: the IF-false branch skips the SET but means
// X already held a value; the IF-true branch runs the default SET. Multi-line
// variants are not matched (accepted until corpus FP volume justifies them).
func testDefaultSetProtections(cfg CFG, src []byte) map[string]int {
	byLine := map[int][]Block{}
	for _, b := range cfg.Blocks {
		if b.Kind == "command" && b.HasCmd {
			byLine[b.Line] = append(byLine[b.Line], b)
		}
	}

	protections := map[string]int{}
	for line, blocks := range byLine {
		for i, b := range blocks {
			if !ifKW[commandKeyword(b.Command, src)] {
				continue
			}
			tested := varsTestedDefensively(b.Command, src)
			if len(tested) == 0 {
				continue
			}
			for _, nb := range blocks[i+1:] {
				if !setKW[commandKeyword(nb.Command, src)] {
					continue
				}
				setTargets := map[string]bool{}
				for _, arg := range argumentNodes(nb.Command) {
					for d := range effectsOfArgument(arg, src, "S").Defs {
						setTargets[d] = true
					}
				}
				for v := range tested {
					if !setTargets[v] {
						continue
					}
					if cur, ok := protections[v]; !ok || line < cur {
						protections[v] = line
					}
				}
			}
		}
	}
	return protections
}

// varsTestedDefensively returns the local-variable names appearing as the first
// argument of $G(...) / $D(...) anywhere in the IF command's argument tree —
// the names the idiom guards.
func varsTestedDefensively(ifCmd parse.Node, src []byte) map[string]bool {
	names := map[string]bool{}
	var visit func(n parse.Node)
	visit = func(n parse.Node) {
		if n.Type() == "function_call" && isDefensiveIntrinsic(n, src) {
			seenFirstVar := false
			for i := uint32(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if !seenFirstVar && c.Type() == "variable" {
					seenFirstVar = true
					if name := firstLocalIdentifier(c, src); name != "" {
						names[name] = true
					}
					continue
				}
				visit(c)
			}
			return
		}
		for i := uint32(0); i < n.ChildCount(); i++ {
			visit(n.Child(i))
		}
	}
	for _, arg := range argumentNodes(ifCmd) {
		visit(arg)
	}
	return names
}

// firstLocalIdentifier returns the identifier of a variable node's
// local_variable child (the tested name), or "" if absent.
func firstLocalIdentifier(variable parse.Node, src []byte) string {
	for i := uint32(0); i < variable.ChildCount(); i++ {
		if lv := variable.Child(i); lv.Type() == "local_variable" {
			return strings.TrimSpace(identifierText(lv, src))
		}
	}
	return ""
}
