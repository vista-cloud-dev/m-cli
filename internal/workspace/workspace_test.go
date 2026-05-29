package workspace_test

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/workspace"
	"github.com/vista-cloud-dev/m-parse/parse"
)

func mustParse(t *testing.T, p *parse.Parser, src string) (parse.Node, func()) {
	t.Helper()
	tree, err := p.Parse(context.Background(), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return tree.RootNode(), tree.Close
}

func newParser(t *testing.T) *parse.Parser {
	t.Helper()
	p, err := parse.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close(context.Background()) })
	return p
}

func TestLabelsAndLookup(t *testing.T) {
	p := newParser(t)
	root, done := mustParse(t, p, "ABC ;\nTAG ;\n Q\nTAG2(X) ;\n Q\n")
	defer done()
	idx := workspace.New()
	idx.AddFile("ABC", root)

	if !idx.HasRoutine("abc") { // case-insensitive
		t.Error("HasRoutine(abc) should be true")
	}
	if idx.HasRoutine("NOPE") {
		t.Error("HasRoutine(NOPE) should be false")
	}
	if !idx.Lookup("ABC", "TAG") || !idx.Lookup("abc", "tag2") {
		t.Error("Lookup should find TAG and TAG2 (case-insensitive)")
	}
	if idx.Lookup("ABC", "MISSING") {
		t.Error("Lookup of a missing label should fail")
	}
	if !idx.Lookup("ABC", "") { // routine entry
		t.Error("Lookup(routine, \"\") should resolve to the entry")
	}
}

func TestReferencesEntryAndExtrinsic(t *testing.T) {
	p := newParser(t)
	src := "CALLER ;\n D TAG^OTHER\n S X=$$FN^OTHER(1)\n D ^THIRD\n Q\n"
	root, done := mustParse(t, p, src)
	defer done()
	refs := workspace.References(root, "CALLER")

	want := map[string]string{} // routine -> label
	for _, r := range refs {
		want[r.TargetRoutine] = r.TargetLabel
	}
	if want["OTHER"] == "" { // TAG^OTHER or FN^OTHER — both label OTHER refs
		t.Errorf("expected a labeled ref into OTHER, got %+v", refs)
	}
	// ^THIRD has no label.
	foundThird := false
	for _, r := range refs {
		if r.TargetRoutine == "THIRD" && r.TargetLabel == "" {
			foundThird = true
		}
	}
	if !foundThird {
		t.Errorf("expected ^THIRD ref with empty label, got %+v", refs)
	}
}

func TestReferencesBareLabel(t *testing.T) {
	p := newParser(t)
	src := "CALLER ;\n D SUB\n Q\nSUB ;\n Q\n"
	root, done := mustParse(t, p, src)
	defer done()
	refs := workspace.References(root, "CALLER")
	// `D SUB` is a bare-label call: ref to (CALLER, SUB).
	found := false
	for _, r := range refs {
		if r.TargetRoutine == "CALLER" && r.TargetLabel == "SUB" {
			found = true
		}
	}
	if !found {
		t.Errorf("bare-label D SUB should ref (CALLER, SUB), got %+v", refs)
	}
}

func TestReferencesTo(t *testing.T) {
	p := newParser(t)
	src := "CALLER ;\n D TAG^OTHER\n Q\n"
	root, done := mustParse(t, p, src)
	defer done()
	idx := workspace.New()
	idx.AddFile("CALLER", root)
	if idx.ReferencesTo("OTHER", "TAG") != 1 {
		t.Errorf("ReferencesTo(OTHER,TAG) = %d, want 1", idx.ReferencesTo("OTHER", "TAG"))
	}
	if idx.ReferencesTo("OTHER", "NOPE") != 0 {
		t.Error("ReferencesTo of an unreferenced label should be 0")
	}
}

func TestUsesRuntimeLabelLookup(t *testing.T) {
	cases := map[string]bool{
		"X ;\n S Y=$T(LBL+1)\n": true,
		"X ;\n D @TAG\n":        true,
		"X ;\n S Y=^DD(9,0)\n":  true,
		"X ;\n D TAG\n Q\n":     false,
	}
	for src, want := range cases {
		if got := workspace.UsesRuntimeLabelLookup([]byte(src)); got != want {
			t.Errorf("UsesRuntimeLabelLookup(%q) = %v, want %v", src, got, want)
		}
	}
}
