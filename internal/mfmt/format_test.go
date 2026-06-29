package mfmt

import (
	"context"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/m-parse/parse"
)

func mustParser(t *testing.T) *parse.Parser {
	t.Helper()
	p, err := parse.New(context.Background())
	if err != nil {
		t.Fatalf("parse.New: %v", err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	return p
}

func TestIdentityIsNoOp(t *testing.T) {
	p := mustParser(t)
	src := []byte("EN ; x\n new a set a=1 write a,!\n quit\n")
	out, err := Format(context.Background(), p, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(src) {
		t.Errorf("identity changed input:\n got %q\nwant %q", out, src)
	}
}

func TestIdentityIgnoresSyntaxErrors(t *testing.T) {
	p := mustParser(t)
	src := []byte("EN ;\n W \"unterminated\n")
	out, err := Format(context.Background(), p, src, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(src) {
		t.Error("identity altered broken input")
	}
}

func TestCanonicalDetabsLeadingWhitespace(t *testing.T) {
	p := mustParser(t)
	src := []byte("EN ;\n\tset x=1\n\t\tquit\n")
	out, err := Format(context.Background(), p, src, Rules(Canonical))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if strings.Contains(got, "\t") {
		t.Errorf("leading tab survived detab:\n%q", got)
	}
	// Shape is preserved (whitespace stays whitespace) and content is intact.
	for _, want := range []string{"\n SET x=1\n", "\n  QUIT\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%q", want, got)
		}
	}
}

func TestCanonicalKeepsTabInsideStringLiteral(t *testing.T) {
	p := mustParser(t)
	// A tab inside a quoted string is data, not indentation — must survive.
	src := []byte("EN ;\n set x=\"a\tb\"\n")
	out, err := Format(context.Background(), p, src, Rules(Canonical))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "\"a\tb\"") {
		t.Errorf("detab corrupted a tab inside a string literal:\n%q", string(out))
	}
}

func TestCanonicalUppercasesKeywordsNotArgs(t *testing.T) {
	p := mustParser(t)
	src := []byte("EN ;\n new x\n set x=1\n write x,!\n quit\n")
	out, err := Format(context.Background(), p, src, Rules(Canonical))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	for _, kw := range []string{"NEW x", "SET x=1", "WRITE x,!", "QUIT"} {
		if !strings.Contains(got, kw) {
			t.Errorf("missing %q in:\n%s", kw, got)
		}
	}
	if strings.Contains(got, "X=1") {
		t.Errorf("argument was uppercased (should be untouched):\n%s", got)
	}
}

func TestCanonicalIdempotent(t *testing.T) {
	p := mustParser(t)
	src := []byte("EN ;\n set x=1 write x quit\n")
	one, err := Format(context.Background(), p, src, Rules(Canonical))
	if err != nil {
		t.Fatal(err)
	}
	two, err := Format(context.Background(), p, one, Rules(Canonical))
	if err != nil {
		t.Fatal(err)
	}
	if string(one) != string(two) {
		t.Errorf("not idempotent:\n first %q\nsecond %q", one, two)
	}
}

// Canonical changes only keyword letter-case, so the parse-tree shape must be
// identical before and after (the AST-preserving contract).
func TestCanonicalPreservesAST(t *testing.T) {
	p := mustParser(t)
	src := []byte("EN ;\n new dfn set dfn=$$first^util(1) quit:dfn'>0  write !,dfn\n")
	out, err := Format(context.Background(), p, src, Rules(Canonical))
	if err != nil {
		t.Fatal(err)
	}
	if string(out) == string(src) {
		t.Fatal("expected canonical to change lowercase commands")
	}
	a, err := p.Parse(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := p.Parse(context.Background(), out)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if !SameShape(a.RootNode(), b.RootNode()) {
		t.Errorf("canonical changed the tree shape\nin:  %s\nout: %s",
			a.RootNode().SExpr(), b.RootNode().SExpr())
	}
}

func TestApplyEditsBasic(t *testing.T) {
	out, err := applyEdits([]byte("abcdef"), []Edit{{Start: 1, End: 3, Replacement: []byte("XY")}})
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "aXYdef" {
		t.Errorf("got %q, want aXYdef", out)
	}
}

func TestApplyEditsOverlapError(t *testing.T) {
	_, err := applyEdits([]byte("abcdef"), []Edit{
		{Start: 0, End: 3, Replacement: []byte("X")},
		{Start: 2, End: 4, Replacement: []byte("Y")},
	})
	if err == nil {
		t.Error("expected an overlapping-edit error")
	}
}

func TestApplyEditsOutOfRange(t *testing.T) {
	if _, err := applyEdits([]byte("abc"), []Edit{{Start: 1, End: 9}}); err == nil {
		t.Error("expected an out-of-range error")
	}
}
