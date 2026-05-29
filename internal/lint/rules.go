package lint

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// Profiles is the set of recognized profile names (for the CLI enum).
//
//	default · modern · pythonic · pedantic — the M-MOD modernization track (tag "modern")
//	xindex  — rules ported from the VA VistA Toolkit ^XINDEX scanner (tag "xindex")
//	sac     — the subset of xindex rules mapping to a documented VA SAC requirement (tag "sac")
//	vista   — VistA-Kernel-specific rules (tag "vista"); pure false positives off VistA
//	all     — every registered rule
var Profiles = []string{"default", "modern", "pythonic", "pedantic", "xindex", "sac", "vista", "all"}

// All returns every registered rule with the built-in default configuration.
func All() []Rule { return AllWith(DefaultOptions()) }

// AllWith returns every registered rule, baking the resolved config into the
// rules that need it (thresholds, Kernel allowlist, taint config). The config-
// neutral rules are returned as-is.
func AllWith(opts Options) []Rule {
	rules := []Rule{
		ruleByRefSubscript,                     // M-MOD-037
		ruleLineLength(opts.Thresholds),        // M-MOD-001
		ruleDotBlockNesting(opts.Thresholds),   // M-MOD-007
		ruleArgumentCount(opts.Thresholds),     // M-MOD-008
		ruleCommandsPerLine(opts.Thresholds),   // M-MOD-009
		ruleStaleTest,                          // M-MOD-017 (flow)
		ruleReadOfUndefined(opts.KernelLocals), // M-MOD-024 (flow)
		ruleLockLeak,                           // M-MOD-025 (flow)
		ruleTransactionLeak,                    // M-MOD-026 (flow)
		ruleEtrapLeak,                          // M-MOD-027 (flow)
		ruleTaintToSink(opts.Taint),            // M-MOD-036 (flow, security)
		ruleAbbreviatedCommand,                 // M-STY-001
	}
	// XINDEX family (tag xindex / sac / vista) — appended so the xindex/sac/vista
	// profiles and `all` select them. The cross-routine rules consume the
	// workspace index at lint time; M-XINDX-007's trusted allowlist is baked in.
	return append(rules, xindexAll(opts.TrustedRoutines)...)
}

// Profile resolves a profile name to its rules with the default config.
func Profile(name string) []Rule { return ProfileWith(name, DefaultOptions()) }

// ProfileWith resolves a profile name to its rules, by tag (spec §3.1):
//
//	default  = modern minus pedantic (the curated, low-noise set)
//	modern   = everything tagged "modern"
//	pythonic = modern (alias for now)
//	pedantic = everything tagged "pedantic"
//	all      = every rule
func ProfileWith(name string, opts Options) []Rule {
	switch name {
	case "all":
		return AllWith(opts)
	case "modern", "pythonic":
		return byTag("modern", opts)
	case "pedantic":
		return byTag("pedantic", opts)
	case "xindex", "sac", "vista":
		return byTag(name, opts)
	default: // "default"
		var out []Rule
		for _, r := range byTag("modern", opts) {
			if !r.hasTag("pedantic") {
				out = append(out, r)
			}
		}
		return out
	}
}

func byTag(tag string, opts Options) []Rule {
	var out []Rule
	for _, r := range AllWith(opts) {
		if r.hasTag(tag) {
			out = append(out, r)
		}
	}
	return out
}

// --- rules -------------------------------------------------------------------

// M-MOD-037 — subscripted by-reference argument (`do f(.x(SUB))`): accepted by
// the grammar but rejected by YottaDB/GT.M at compile time. Portability error.
var ruleByRefSubscript = Rule{
	ID:       "M-MOD-037",
	Severity: Error,
	Category: "portability",
	Title:    "Subscripted by-reference parameter is invalid YottaDB/GT.M syntax",
	Tags:     []string{"modern"},
	Query:    "(by_reference (subscripts)) @ref",
	OnMatch: func(m parse.Match, _ []byte) (string, bool) {
		return "subscripted by-reference parameter `" + string(m.Captures[0].Node.Text()) +
			"` is rejected by YottaDB/GT.M — pass the whole local, or merge the subtree into a temp", true
	},
}

// M-MOD-001 — line longer than the configured column limit.
func ruleLineLength(th Thresholds) Rule {
	return Rule{
		ID:       "M-MOD-001",
		Severity: Style,
		Category: "style",
		Title:    "Line longer than configured limit",
		Tags:     []string{"modern"},
		Inspect: func(_ parse.Node, src []byte) []Finding {
			var out []Finding
			for i, line := range strings.Split(string(src), "\n") {
				n := utf8.RuneCountInString(line)
				if n > th.LineLength {
					out = append(out, Finding{
						Message: fmt.Sprintf("line is %d columns (limit: %d)", n, th.LineLength),
						Line:    i + 1, Col: th.LineLength + 1, EndLine: i + 1, EndCol: n + 1,
					})
				}
			}
			return out
		},
	}
}

// M-MOD-007 — dot-block nesting depth exceeds the configured limit.
func ruleDotBlockNesting(th Thresholds) Rule {
	return Rule{
		ID:       "M-MOD-007",
		Severity: Warning,
		Category: "complexity",
		Title:    "Dot-block nesting depth exceeds configured limit",
		Tags:     []string{"modern"},
		Inspect: func(root parse.Node, _ []byte) []Finding {
			var out []Finding
			walkNodes(root, func(n parse.Node) {
				if n.Type() != "dot_block_prefix" {
					return
				}
				depth := bytes.Count(n.Text(), []byte("."))
				if depth > th.DotBlockDepth {
					s, e := n.StartPoint(), n.EndPoint()
					out = append(out, Finding{
						Message: fmt.Sprintf("dot-block nesting depth %d (limit: %d)", depth, th.DotBlockDepth),
						Line:    int(s.Row) + 1, Col: int(s.Column) + 1, EndLine: int(e.Row) + 1, EndCol: int(e.Column) + 1,
					})
				}
			})
			return out
		},
	}
}

// M-MOD-008 — a label has more formal arguments than the configured limit.
func ruleArgumentCount(th Thresholds) Rule {
	return Rule{
		ID:       "M-MOD-008",
		Severity: Warning,
		Category: "complexity",
		Title:    "Argument count exceeds configured limit",
		Tags:     []string{"modern"},
		Inspect: func(root parse.Node, _ []byte) []Finding {
			var out []Finding
			walkNodes(root, func(n parse.Node) {
				if n.Type() != "formals" {
					return
				}
				args := 0
				for i := uint32(0); i < n.ChildCount(); i++ {
					if n.Child(i).Type() == "identifier" {
						args++
					}
				}
				if args <= th.ArgumentCount {
					return
				}
				s := n.StartPoint()
				out = append(out, Finding{
					Message: fmt.Sprintf("label has %d formal arguments (limit: %d)", args, th.ArgumentCount),
					Line:    int(s.Row) + 1, Col: int(s.Column) + 1, EndLine: int(s.Row) + 1, EndCol: int(s.Column) + 1,
				})
			})
			return out
		},
	}
}

// M-MOD-009 — more than the configured number of commands on one line.
func ruleCommandsPerLine(th Thresholds) Rule {
	return Rule{
		ID:       "M-MOD-009",
		Severity: Style,
		Category: "style",
		Title:    "Too many commands on a single line",
		Tags:     []string{"modern", "pedantic"},
		Inspect: func(root parse.Node, _ []byte) []Finding {
			var out []Finding
			walkNodes(root, func(n parse.Node) {
				if n.Type() != "command_sequence" {
					return
				}
				cmds := 0
				for i := uint32(0); i < n.ChildCount(); i++ {
					if n.Child(i).Type() == "command" {
						cmds++
					}
				}
				if cmds <= th.CommandsLine {
					return
				}
				s := n.StartPoint()
				out = append(out, Finding{
					Message: fmt.Sprintf("%d commands on one line (limit: %d)", cmds, th.CommandsLine),
					Line:    int(s.Row) + 1, Col: int(s.Column) + 1, EndLine: int(s.Row) + 1, EndCol: int(s.Column) + 1,
				})
			})
			return out
		},
	}
}

// M-STY-001 — abbreviated single-letter command keyword (provisional Go-side id;
// the modern style prefers the full word). Pedantic, so not in the default set.
var ruleAbbreviatedCommand = Rule{
	ID:       "M-STY-001",
	Severity: Style,
	Category: "style",
	Title:    "Command keyword is abbreviated",
	Tags:     []string{"modern", "pedantic"},
	Query:    "(command_keyword) @kw",
	OnMatch: func(m parse.Match, _ []byte) (string, bool) {
		kw := bytes.TrimSpace(m.Captures[0].Node.Text())
		if len(kw) != 1 {
			return "", false
		}
		return "abbreviated command keyword `" + string(kw) + "`; modern style prefers the full word", true
	},
}

// walkNodes visits n and every descendant in pre-order.
func walkNodes(n parse.Node, fn func(parse.Node)) {
	fn(n)
	for i := uint32(0); i < n.ChildCount(); i++ {
		walkNodes(n.Child(i), fn)
	}
}
