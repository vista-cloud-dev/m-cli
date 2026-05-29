package lint

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// Default thresholds for the metric rules (mirrors the Python m-cli defaults).
// Config plumbing ([lint.thresholds] / --threshold) is a follow-up.
const (
	thLineLength    = 200
	thDotBlockDepth = 5
	thArgumentCount = 7
	thCommandsLine  = 3
)

// Profiles is the set of recognized profile names (for the CLI enum). sac /
// vista / xindex are reserved for when those rule families land (they'd select
// rules tagged accordingly); today they'd be empty, so they're not exposed yet.
var Profiles = []string{"default", "modern", "pythonic", "pedantic", "all"}

// All returns every registered rule.
func All() []Rule {
	return []Rule{
		ruleByRefSubscript,     // M-MOD-037
		ruleLineLength,         // M-MOD-001
		ruleDotBlockNesting,    // M-MOD-007
		ruleArgumentCount,      // M-MOD-008
		ruleCommandsPerLine,    // M-MOD-009
		ruleLockLeak,           // M-MOD-025 (flow)
		ruleTransactionLeak,    // M-MOD-026 (flow)
		ruleEtrapLeak,          // M-MOD-027 (flow)
		ruleAbbreviatedCommand, // M-STY-001
	}
}

// Profile resolves a profile name to its rules, by tag (spec §3.1):
//
//	default  = modern minus pedantic (the curated, low-noise set)
//	modern   = everything tagged "modern"
//	pythonic = modern (alias for now)
//	pedantic = everything tagged "pedantic"
//	all      = every rule
func Profile(name string) []Rule {
	switch name {
	case "all":
		return All()
	case "modern", "pythonic":
		return byTag("modern")
	case "pedantic":
		return byTag("pedantic")
	default: // "default"
		var out []Rule
		for _, r := range byTag("modern") {
			if !r.hasTag("pedantic") {
				out = append(out, r)
			}
		}
		return out
	}
}

func byTag(tag string) []Rule {
	var out []Rule
	for _, r := range All() {
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
var ruleLineLength = Rule{
	ID:       "M-MOD-001",
	Severity: Style,
	Category: "style",
	Title:    "Line longer than configured limit",
	Tags:     []string{"modern"},
	Inspect: func(_ parse.Node, src []byte) []Finding {
		var out []Finding
		for i, line := range strings.Split(string(src), "\n") {
			n := utf8.RuneCountInString(line)
			if n > thLineLength {
				out = append(out, Finding{
					Message: fmt.Sprintf("line is %d columns (limit: %d)", n, thLineLength),
					Line:    i + 1, Col: thLineLength + 1, EndLine: i + 1, EndCol: n + 1,
				})
			}
		}
		return out
	},
}

// M-MOD-007 — dot-block nesting depth exceeds the configured limit.
var ruleDotBlockNesting = Rule{
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
			if depth > thDotBlockDepth {
				s, e := n.StartPoint(), n.EndPoint()
				out = append(out, Finding{
					Message: fmt.Sprintf("dot-block nesting depth %d (limit: %d)", depth, thDotBlockDepth),
					Line:    int(s.Row) + 1, Col: int(s.Column) + 1, EndLine: int(e.Row) + 1, EndCol: int(e.Column) + 1,
				})
			}
		})
		return out
	},
}

// M-MOD-008 — a label has more formal arguments than the configured limit.
var ruleArgumentCount = Rule{
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
			if args <= thArgumentCount {
				return
			}
			s := n.StartPoint()
			out = append(out, Finding{
				Message: fmt.Sprintf("label has %d formal arguments (limit: %d)", args, thArgumentCount),
				Line:    int(s.Row) + 1, Col: int(s.Column) + 1, EndLine: int(s.Row) + 1, EndCol: int(s.Column) + 1,
			})
		})
		return out
	},
}

// M-MOD-009 — more than the configured number of commands on one line.
var ruleCommandsPerLine = Rule{
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
			if cmds <= thCommandsLine {
				return
			}
			s := n.StartPoint()
			out = append(out, Finding{
				Message: fmt.Sprintf("%d commands on one line (limit: %d)", cmds, thCommandsLine),
				Line:    int(s.Row) + 1, Col: int(s.Column) + 1, EndLine: int(s.Row) + 1, EndCol: int(s.Column) + 1,
			})
		})
		return out
	},
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
