// Package mfmt is m-cli's AST-preserving M source formatter (spec §3.1). It
// folds in vista-meta's mfmt as one formatter over the m-parse syntax tree.
//
// Model: edits-over-source. Each rule emits byte-span Edits guided by the parse
// tree; the edits are applied to the original bytes. The default (no rules) is
// identity — unformatted input is returned byte-for-byte, so the round-trip is
// exact and formatting is opt-in (mirrors the Python m-cli's identity default +
// canonical layer). Rules are written to be AST-preserving; SameShape verifies
// that parse(format(src)) has the same tree shape as parse(src).
package mfmt

import (
	"context"
	"fmt"
	"sort"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// Edit replaces src[Start:End) with Replacement.
type Edit struct {
	Start       uint32
	End         uint32
	Replacement []byte
}

// Rule is one formatting transform: given the source and its parsed root, it
// returns the byte-span edits it wants applied.
type Rule interface {
	Name() string
	Edits(src []byte, root parse.Node) []Edit
}

// Format parses src, runs the rules, and applies their edits. With no rules it
// returns src unchanged (identity) without parsing.
func Format(ctx context.Context, p *parse.Parser, src []byte, rules []Rule) ([]byte, error) {
	if len(rules) == 0 {
		return append([]byte(nil), src...), nil
	}
	tree, err := p.Parse(ctx, src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.RootNode()

	var edits []Edit
	for _, r := range rules {
		edits = append(edits, r.Edits(src, root)...)
	}
	return applyEdits(src, edits)
}

// applyEdits applies non-overlapping edits to src (lowest offset first). It
// errors on overlap or an out-of-range span rather than silently corrupting.
func applyEdits(src []byte, edits []Edit) ([]byte, error) {
	if len(edits) == 0 {
		return append([]byte(nil), src...), nil
	}
	sort.SliceStable(edits, func(i, j int) bool {
		if edits[i].Start != edits[j].Start {
			return edits[i].Start < edits[j].Start
		}
		return edits[i].End < edits[j].End
	})

	out := make([]byte, 0, len(src))
	var pos uint32
	for _, e := range edits {
		if e.Start > e.End || int(e.End) > len(src) {
			return nil, fmt.Errorf("mfmt: edit span [%d,%d) out of range (len %d)", e.Start, e.End, len(src))
		}
		if e.Start < pos {
			return nil, fmt.Errorf("mfmt: overlapping edit at %d (cursor already at %d)", e.Start, pos)
		}
		out = append(out, src[pos:e.Start]...)
		out = append(out, e.Replacement...)
		pos = e.End
	}
	out = append(out, src[pos:]...)
	return out, nil
}
