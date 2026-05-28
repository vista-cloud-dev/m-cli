package mfmt

import "github.com/vista-cloud-dev/m-parse/parse"

// SameShape reports whether two trees have the same structure — same node
// types and named-ness, recursively, with the same child counts — ignoring
// byte positions and token text. It is the AST-preserving check: a formatting
// rule is safe iff parse(format(src)) is SameShape as parse(src).
func SameShape(a, b parse.Node) bool {
	if a.Type() != b.Type() || a.IsNamed() != b.IsNamed() {
		return false
	}
	if a.ChildCount() != b.ChildCount() {
		return false
	}
	for i := uint32(0); i < a.ChildCount(); i++ {
		if !SameShape(a.Child(i), b.Child(i)) {
			return false
		}
	}
	return true
}
