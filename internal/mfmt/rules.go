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
		return []Rule{UppercaseCommandKeywords{}, DetabLeadingWhitespace{}}
	default:
		return nil // identity
	}
}

// DetabLeadingWhitespace converts each tab in a line's leading-whitespace run to
// a single space (mirroring how an engine flattens a leading tab at install).
// It touches ONLY the indentation region — a tab inside a string literal or a
// comment is data and is left untouched — so the parse-tree shape is preserved.
// This is the auto-fix companion to lint M-MOD-039 (SAC + modern: spaces only).
type DetabLeadingWhitespace struct{}

// Name implements Rule.
func (DetabLeadingWhitespace) Name() string { return "detab-leading-whitespace" }

// Edits implements Rule.
func (DetabLeadingWhitespace) Edits(src []byte, _ parse.Node) []Edit {
	var edits []Edit
	atLineStart := true
	for i := 0; i < len(src); i++ {
		switch {
		case src[i] == '\n':
			atLineStart = true
		case !atLineStart:
			// past the indentation region; ignore tabs in code/strings/comments
		case src[i] == '\t':
			edits = append(edits, Edit{Start: uint32(i), End: uint32(i + 1), Replacement: []byte(" ")})
		case src[i] == ' ':
			// still in the leading-whitespace run
		default:
			atLineStart = false // first non-whitespace byte on the line
		}
	}
	return edits
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
