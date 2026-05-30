package mtest_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/mtest"
)

// ReferencedRoutines returns the external routines a suite calls — the basis
// for mapping a changed routine to the suites that exercise it.
func TestReferencedRoutines(t *testing.T) {
	src := []byte(`MATHTST ; math suite
 do start^STDASSERT(.pass,.fail)
 do tAdd(.pass,.fail)
 do report^STDASSERT(pass,fail)
 quit
tAdd(pass,fail) ;@TEST "add"
 do eq^STDASSERT(.pass,.fail,$$add^MATH(2,3),5,"2+3")
 do helper
 quit
helper ;
 do log^STDLOG("done")
 quit
`)
	got, err := mtest.ReferencedRoutines(mustParser(t), src)
	if err != nil {
		t.Fatal(err)
	}
	// External targets only, sorted; local label calls (tAdd, helper) excluded.
	want := []string{"MATH", "STDASSERT", "STDLOG"}
	if len(got) != len(want) {
		t.Fatalf("ReferencedRoutines = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("ReferencedRoutines = %v, want %v", got, want)
		}
	}
}

// Affected picks the suites that exercise any changed routine: the suite's own
// routine changed, or it references a changed routine.
func TestAffected(t *testing.T) {
	suites := []mtest.TestSuite{
		{Name: "MATHTST", Deps: []string{"MATH", "STDASSERT"}},
		{Name: "STRTST", Deps: []string{"STRING", "STDASSERT"}},
	}
	names := func(ss []mtest.TestSuite) []string {
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = s.Name
		}
		return out
	}
	cases := []struct {
		desc    string
		changed map[string]bool
		want    []string
	}{
		{"dep of one suite", map[string]bool{"MATH": true}, []string{"MATHTST"}},
		{"shared dep hits both", map[string]bool{"STDASSERT": true}, []string{"MATHTST", "STRTST"}},
		{"suite's own routine", map[string]bool{"STRTST": true}, []string{"STRTST"}},
		{"unrelated routine", map[string]bool{"NOPE": true}, nil},
	}
	for _, c := range cases {
		got := names(mtest.Affected(suites, c.changed))
		if len(got) != len(c.want) {
			t.Errorf("%s: Affected = %v, want %v", c.desc, got, c.want)
			continue
		}
		for i := range c.want {
			if got[i] != c.want[i] {
				t.Errorf("%s: Affected = %v, want %v", c.desc, got, c.want)
				break
			}
		}
	}
}

// Discover records each suite's external dependencies so the watch loop can do
// affected-test selection without re-parsing on every save.
func TestDiscoverDeps(t *testing.T) {
	dir := t.TempDir()
	src, _ := os.ReadFile("testdata/SAMPLETST.m")
	if err := os.WriteFile(filepath.Join(dir, "SAMPLETST.m"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	suites, err := mtest.Discover(mustParser(t), []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(suites) != 1 {
		t.Fatalf("got %d suites, want 1", len(suites))
	}
	if len(suites[0].Deps) != 1 || suites[0].Deps[0] != "STDASSERT" {
		t.Errorf("Deps = %v, want [STDASSERT]", suites[0].Deps)
	}
}
