package lint

import (
	"bytes"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// Profiles is the set of recognized profile names (for the CLI enum). The full
// 8-profile scheme (sac/vista/xindex/modern/pythonic/pedantic/default/all)
// arrives with the rule-set port; these are the ones the starter rules use.
var Profiles = []string{"default", "modern", "all"}

// All returns the full starter rule set. (Full M-MOD parity follows, gated by
// the corpus.)
func All() []Rule {
	return []Rule{ruleByRefSubscript, ruleAbbreviatedCommand}
}

// Profile returns the rules tagged with the named profile.
func Profile(name string) []Rule {
	var out []Rule
	for _, r := range All() {
		for _, p := range r.Profiles {
			if p == name {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// ruleByRefSubscript — M-MOD-037. A subscripted by-reference argument
// (`do f(.x(SUB))`) is accepted by the grammar but rejected by YottaDB/GT.M at
// compile time (%YDB-E-COMMAORRPAREXP). Portability error.
var ruleByRefSubscript = Rule{
	ID:       "M-MOD-037",
	Severity: Error,
	Profiles: []string{"default", "modern", "all"},
	Query:    "(by_reference (subscripts)) @ref",
	Doc:      "subscripted by-reference parameter is invalid YottaDB/GT.M syntax",
	Check: func(m parse.Match, _ []byte) (string, bool) {
		ref := string(m.Captures[0].Node.Text())
		return "subscripted by-reference parameter `" + ref +
			"` is rejected by YottaDB/GT.M — pass the whole local, or merge the subtree into a temp", true
	},
}

// ruleAbbreviatedCommand — provisional Go-side style rule (ID to be reconciled
// with the canonical catalog). Flags single-letter command keywords; the modern
// style prefers the full word. Not in the default profile (VistA/SAC style
// deliberately uses the compact form).
var ruleAbbreviatedCommand = Rule{
	ID:       "M-STY-001",
	Severity: Style,
	Profiles: []string{"modern", "all"},
	Query:    "(command_keyword) @kw",
	Doc:      "command keyword is abbreviated; the modern style prefers the full word",
	Check: func(m parse.Match, _ []byte) (string, bool) {
		kw := bytes.TrimSpace(m.Captures[0].Node.Text())
		if len(kw) != 1 {
			return "", false
		}
		return "abbreviated command keyword `" + string(kw) + "`; modern style prefers the full word", true
	},
}
