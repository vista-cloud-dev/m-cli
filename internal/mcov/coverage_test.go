package mcov

import (
	"context"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
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

func TestBuildScript(t *testing.T) {
	s := BuildScript([]string{"FOOTST", "BARTST"})
	for _, want := range []string{
		"kill ^ycov", `view "TRACE":1:"^ycov":""`, "do ^FOOTST", "do ^BARTST",
		`view "TRACE":0:"^ycov":""`, "zwrite ^ycov", "halt",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script missing %q:\n%s", want, s)
		}
	}
	// trace must be enabled before the suites and disabled after.
	if strings.Index(s, `:1:`) > strings.Index(s, "do ^FOOTST") {
		t.Error("trace not enabled before suites")
	}
}

func TestParseYcov(t *testing.T) {
	dump := `^ycov("*RUN")="1:2:3"
^ycov("MATH","add")="9:0:1:1:1"
^ycov("MATH","add",1)="3:0:1:1:1"
^ycov("MATH","sub",1)="0:0:1:1:1"`
	hits := parseYcov(dump)
	// Only the 3-subscript entries count; *RUN and the 2-subscript total are out.
	if len(hits) != 2 {
		t.Fatalf("got %d entries %v, want 2", len(hits), hits)
	}
	if hits[ycovKey{"MATH", "ADD", 1}] != 3 {
		t.Errorf("MATH/add/1 = %d, want 3", hits[ycovKey{"MATH", "ADD", 1}])
	}
	if hits[ycovKey{"MATH", "SUB", 1}] != 0 {
		t.Errorf("MATH/sub/1 = %d, want 0", hits[ycovKey{"MATH", "SUB", 1}])
	}
}

func TestDiscoverExecutables(t *testing.T) {
	execs, err := DiscoverExecutables(mustParser(t), []string{"testdata/MATH.m"})
	if err != nil {
		t.Fatal(err)
	}
	// Two executable lines: `quit a+b` (add, offset 1) and `quit a-b` (sub, offset 1).
	if len(execs) != 2 {
		t.Fatalf("got %d exec lines %+v, want 2", len(execs), execs)
	}
	want := map[string]ExecLine{
		"add": {Routine: "MATH", Label: "add", Path: "testdata/MATH.m", Line: 3, Offset: 1},
		"sub": {Routine: "MATH", Label: "sub", Path: "testdata/MATH.m", Line: 5, Offset: 1},
	}
	for _, e := range execs {
		w := want[e.Label]
		if e != w {
			t.Errorf("exec %q = %+v, want %+v", e.Label, e, w)
		}
	}
}

// fakeEngine returns a canned ^ycov dump from RunScript.
type fakeEngine struct{ dump string }

func (fakeEngine) Kind() engine.Kind                          { return engine.YDB }
func (fakeEngine) EnsureLoaded(context.Context, string) error { return nil }
func (fakeEngine) RunRoutine(context.Context, string, ...string) (engine.Result, error) {
	return engine.Result{}, nil
}
func (fakeEngine) RunXCmd(context.Context, string) (engine.Result, error) {
	return engine.Result{}, nil
}
func (f fakeEngine) RunScript(context.Context, string) (engine.Result, error) {
	return engine.Result{Stdout: f.dump}, nil
}

func TestRunJoinsHits(t *testing.T) {
	// add is hit (offset 1), sub is not → 1/2 covered, 50%.
	dump := `^ycov("MATH","add",1)="4:0:1:1:1"`
	r, err := Run(context.Background(), mustParser(t), fakeEngine{dump: dump}, []string{"testdata/MATH.m"}, []string{"MATHTST"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total() != 2 || r.Covered() != 1 {
		t.Fatalf("coverage = %d/%d, want 1/2", r.Covered(), r.Total())
	}
	if r.Percent() != 50 {
		t.Errorf("percent = %.1f, want 50", r.Percent())
	}
	for _, l := range r.Lines {
		if l.Label == "add" && l.Hits != 4 {
			t.Errorf("add hits = %d, want 4", l.Hits)
		}
		if l.Label == "sub" && l.Hits != 0 {
			t.Errorf("sub hits = %d, want 0", l.Hits)
		}
	}
}

func TestLCOV(t *testing.T) {
	r := Result{Lines: []LineCov{
		{Routine: "MATH", Label: "add", Path: "MATH.m", Line: 3, Hits: 4},
		{Routine: "MATH", Label: "sub", Path: "MATH.m", Line: 5, Hits: 0},
	}}
	out := LCOV(r)
	for _, want := range []string{"SF:MATH.m", "DA:3,4", "DA:5,0", "LF:2", "LH:1", "end_of_record"} {
		if !strings.Contains(out, want) {
			t.Errorf("LCOV missing %q:\n%s", want, out)
		}
	}
}
