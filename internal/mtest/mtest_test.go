package mtest_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
	"github.com/vista-cloud-dev/m-cli/internal/mtest"
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

func TestIsSuiteFile(t *testing.T) {
	cases := map[string]bool{
		"SAMPLETST.m": true, "DGREGTST.mac": true, "XUTST.int": true,
		"SAMPLE.m": false, "sampletst.m": false, "TST.m": false, "notes.txt": false,
	}
	for name, want := range cases {
		if got := mtest.IsSuiteFile(name); got != want {
			t.Errorf("IsSuiteFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDetectProtocol(t *testing.T) {
	if p := mtest.DetectProtocol([]byte(" do start^STDASSERT(.pass,.fail)\n")); p != "STDASSERT" {
		t.Errorf("protocol = %q, want STDASSERT", p)
	}
	if p := mtest.DetectProtocol([]byte(" w 1\n")); p != "TESTRUN" {
		t.Errorf("protocol = %q, want TESTRUN (default)", p)
	}
}

func TestDetectTier(t *testing.T) {
	cases := map[string]string{
		"FOOTST\t; suite\n\t;; tier: integration\n":   mtest.TierIntegration,
		"FOOTST\t; suite\n\t; tier: integration\n":    mtest.TierIntegration,
		"FOOTST\t;; tier:integration\n":               mtest.TierIntegration,
		"FOOTST\t;; tier: pure-logic\n":               mtest.TierPureLogic,
		"FOOTST\t; an ordinary suite, no directive\n": mtest.TierPureLogic,   // untagged ⇒ safe default
		"FOOTST\t;; TIER: Integration\n":              mtest.TierIntegration, // case-insensitive
	}
	for src, want := range cases {
		if got := mtest.DetectTier([]byte(src)); got != want {
			t.Errorf("DetectTier(%q) = %q, want %q", src, got, want)
		}
	}
}

func TestFindCases(t *testing.T) {
	src, err := os.ReadFile("testdata/SAMPLETST.m")
	if err != nil {
		t.Fatal(err)
	}
	cases, err := mtest.FindCases(mustParser(t), "SAMPLETST", src)
	if err != nil {
		t.Fatal(err)
	}
	// tAddsTwo + tGreets; SAMPLETST entry and helper(x) excluded.
	if len(cases) != 2 {
		t.Fatalf("got %d cases %+v, want 2", len(cases), cases)
	}
	if cases[0].Label != "tAddsTwo" || cases[0].Description != "two plus two is four" {
		t.Errorf("case[0] = %+v, want tAddsTwo / 'two plus two is four'", cases[0])
	}
	if cases[1].Label != "tGreets" || cases[1].Description != "" {
		t.Errorf("case[1] = %+v, want tGreets / ''", cases[1])
	}
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	src, _ := os.ReadFile("testdata/SAMPLETST.m")
	if err := os.WriteFile(filepath.Join(dir, "SAMPLETST.m"), src, 0o644); err != nil {
		t.Fatal(err)
	}
	// a non-suite file must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "PLAIN.m"), []byte("EN ;\n quit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	suites, err := mtest.Discover(mustParser(t), []string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(suites) != 1 || suites[0].Name != "SAMPLETST" {
		t.Fatalf("suites = %+v, want one SAMPLETST", suites)
	}
	if suites[0].Protocol != "STDASSERT" || len(suites[0].Cases) != 2 {
		t.Errorf("suite = %+v, want protocol STDASSERT + 2 cases", suites[0])
	}
}

func TestParseOutputPass(t *testing.T) {
	out := "  PASS  adds\n  PASS  greet\n\nResults: 2 tests  2 passed  0 failed\nAll tests passed.\n"
	s := mtest.ParseOutput(out)
	if !s.OK || s.Total != 2 || s.Passed != 2 || s.Failed != 0 {
		t.Errorf("summary = %+v, want ok 2/2/0", s)
	}
	if len(s.Assertions) != 2 || s.Assertions[0].Outcome != mtest.Pass {
		t.Errorf("assertions = %+v", s.Assertions)
	}
}

func TestParseOutputFail(t *testing.T) {
	out := "  PASS  adds\n  FAIL  greet\n         expected: hi\n         actual: bye\n\nResults: 2 tests  1 passed  1 failed\n1 test(s) FAILED.\n"
	s := mtest.ParseOutput(out)
	if s.OK || s.Failed != 1 || s.Passed != 1 {
		t.Errorf("summary = %+v, want not-ok 1 passed 1 failed", s)
	}
	var fail *mtest.Assertion
	for i := range s.Assertions {
		if s.Assertions[i].Outcome == mtest.Fail {
			fail = &s.Assertions[i]
		}
	}
	if fail == nil || fail.Expected != "hi" || fail.Actual != "bye" {
		t.Errorf("fail assertion = %+v, want expected hi / actual bye", fail)
	}
}

// fakeEngine returns canned output, recording what was run.
type fakeEngine struct {
	out     string
	exit    int
	ran     []string
	loaded  []string
	loadErr error
	runErr  error
}

func (f *fakeEngine) Kind() engine.Kind { return engine.YDB }
func (f *fakeEngine) EnsureLoaded(_ context.Context, path string) error {
	f.loaded = append(f.loaded, path)
	return f.loadErr
}
func (f *fakeEngine) RunRoutine(_ context.Context, entryref string, _ ...string) (engine.Result, error) {
	f.ran = append(f.ran, entryref)
	return engine.Result{Stdout: f.out, ExitCode: f.exit}, f.runErr
}
func (f *fakeEngine) RunXCmd(_ context.Context, _ string) (engine.Result, error) {
	return engine.Result{}, nil
}
func (f *fakeEngine) RunScript(_ context.Context, _ string) (engine.Result, error) {
	return engine.Result{}, nil
}

func TestRunSuiteOK(t *testing.T) {
	f := &fakeEngine{out: "Results: 1 tests  1 passed  0 failed\nAll tests passed.\n"}
	r, err := mtest.RunSuite(context.Background(), f, mtest.TestSuite{Name: "SAMPLETST", Path: "/x/SAMPLETST.m"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.OK || r.Summary.Passed != 1 {
		t.Errorf("result = %+v, want ok 1 passed", r)
	}
	if len(f.ran) != 1 || f.ran[0] != "^SAMPLETST" {
		t.Errorf("ran = %v, want [^SAMPLETST]", f.ran)
	}
}

// A non-zero engine exit must make the suite fail even if the summary parsed ok.
func TestRunSuiteNonZeroExitFails(t *testing.T) {
	f := &fakeEngine{out: "Results: 1 tests  1 passed  0 failed\nAll tests passed.\n", exit: 1}
	r, err := mtest.RunSuite(context.Background(), f, mtest.TestSuite{Name: "X", Path: "/x/X.m"})
	if err != nil {
		t.Fatal(err)
	}
	if r.OK {
		t.Error("suite reported ok despite a non-zero engine exit")
	}
}
