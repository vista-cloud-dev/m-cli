package mfmt

import (
	"bytes"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// Preset is a named bundle of rules. identity (default) is a no-op so
// formatting is opt-in; canonical is the SAC-leaning baseline. More presets
// (pythonic, pythonic-lower, compact, sac) follow as the rule set grows.
type Preset string

const (
	Identity  Preset = "identity"
	Canonical Preset = "canonical"
)

// Presets is the set of recognized preset names (for the CLI enum).
var Presets = []string{string(Identity), string(Canonical)}

// Rules returns the rules for a preset.
func Rules(p Preset) []Rule {
	switch p {
	case Canonical:
		return []Rule{UppercaseCommandKeywords{}}
	default:
		return nil // identity
	}
}

// UppercaseCommandKeywords uppercases every command keyword token (set→SET,
// w→W). AST-preserving: only the letters inside command_keyword tokens change,
// so the parse-tree shape is unaffected.
type UppercaseCommandKeywords struct{}

// Name implements Rule.
func (UppercaseCommandKeywords) Name() string { return "uppercase-command-keywords" }

// Edits implements Rule.
func (UppercaseCommandKeywords) Edits(src []byte, root parse.Node) []Edit {
	var edits []Edit
	var walk func(parse.Node)
	walk = func(n parse.Node) {
		if n.Type() == "command_keyword" {
			s, e := n.StartByte(), n.EndByte()
			if int(e) > len(src) || s > e {
				return
			}
			orig := src[s:e]
			up := bytes.ToUpper(orig)
			if !bytes.Equal(orig, up) {
				edits = append(edits, Edit{Start: s, End: e, Replacement: up})
			}
			return // a keyword is a leaf token
		}
		for i := uint32(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(root)
	return edits
}
