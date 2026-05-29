package flow

import (
	"context"
	"sort"
	"testing"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// parseFirstCommand parses "EN ;\n <body>\n q\n" and returns the first command
// node on the body line (row 1).
func parseFirstCommand(t *testing.T, body string) (parse.Node, []byte, func()) {
	t.Helper()
	src := []byte("EN ;\n " + body + "\n q\n")
	p, err := parse.New(context.Background())
	if err != nil {
		t.Fatalf("parse.New: %v", err)
	}
	tr, err := p.Parse(context.Background(), src)
	if err != nil {
		_ = p.Close(context.Background())
		t.Fatalf("Parse: %v", err)
	}
	cleanup := func() { tr.Close(); _ = p.Close(context.Background()) }
	var found parse.Node
	var ok bool
	var walk func(n parse.Node)
	walk = func(n parse.Node) {
		if ok {
			return
		}
		if n.Type() == "command" && int(n.StartPoint().Row) == 1 {
			found, ok = n, true
			return
		}
		for i := uint32(0); i < n.ChildCount(); i++ {
			walk(n.Child(i))
		}
	}
	walk(tr.RootNode())
	if !ok {
		cleanup()
		t.Fatalf("no command found on row 1 of %q", src)
	}
	return found, src, cleanup
}

func sortedKeys(s map[string]bool) []string {
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func useNames(uses []VarUse) []string {
	out := make([]string, 0, len(uses))
	for _, u := range uses {
		out = append(out, u.Name)
	}
	return out
}

func eqSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEffects(t *testing.T) {
	cases := []struct {
		body     string
		defs     []string
		kills    []string
		killsAll bool
		uses     []string // ordered
	}{
		{body: "set X=1", defs: []string{"X"}},
		{body: "set X=A", defs: []string{"X"}, uses: []string{"A"}},
		{body: "set X=1,Y=X", defs: []string{"X", "Y"}, uses: []string{"X"}},
		{body: "set A(I,J)=B(K)", defs: []string{"A"}, uses: []string{"I", "J", "B", "K"}}, // A's subscripts, then RHS B and its subscript
		{body: "kill A", kills: []string{"A"}},
		{body: "kill A,B(C)", kills: []string{"A"}, uses: []string{"C"}}, // B(C) is a partial kill: base stays, C is a read
		{body: "new A", kills: []string{"A"}},
		{body: "new A,$etrap", kills: []string{"A"}}, // $etrap is a special_variable, not a local
		{body: "kill", killsAll: true},
		{body: "new", killsAll: true},
		{body: "for I=1:1:10", defs: []string{"I"}},
		{body: "write $G(X),Z", uses: []string{"Z"}},      // $G(X) is a defensive read — X is suppressed
		{body: "set X=$$F(.Y)", defs: []string{"X", "Y"}}, // .Y by-reference defines Y in the caller frame
		{body: "do LBL(.X,Y)", defs: []string{"X"}, uses: []string{"Y"}},
		{body: "write:X Y", uses: []string{"X", "Y"}}, // postcond X, then arg Y
		{body: "merge A=B", defs: []string{"A"}, uses: []string{"B"}},
		{body: "read X", defs: []string{"X"}},
	}
	for _, c := range cases {
		t.Run(c.body, func(t *testing.T) {
			cmd, src, done := parseFirstCommand(t, c.body)
			defer done()
			eff := effects(cmd, src)
			if got := sortedKeys(eff.Defs); !eqSlice(got, c.defs) {
				t.Errorf("defs = %v, want %v", got, c.defs)
			}
			if got := sortedKeys(eff.Kills); !eqSlice(got, c.kills) {
				t.Errorf("kills = %v, want %v", got, c.kills)
			}
			if eff.KillsAll != c.killsAll {
				t.Errorf("killsAll = %v, want %v", eff.KillsAll, c.killsAll)
			}
			if got := useNames(eff.Uses); !eqSlice(got, c.uses) {
				t.Errorf("uses = %v, want %v", got, c.uses)
			}
		})
	}
}

func TestFormalParams(t *testing.T) {
	src := []byte("LBL(A,B) ;\n quit\nNF ;\n quit\n")
	p, err := parse.New(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close(context.Background()) }()
	tr, err := p.Parse(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	fp := FormalParams(tr.RootNode(), src)
	if got := fp[0]; !eqSlice(got, []string{"A", "B"}) {
		t.Errorf("formals[row 0] = %v, want [A B]", got)
	}
	if _, ok := fp[2]; ok {
		t.Errorf("label with no formals should not appear, got %v", fp[2])
	}
}
