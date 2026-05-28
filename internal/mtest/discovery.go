package mtest

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// TestCase is one test label inside a suite.
type TestCase struct {
	Suite       string
	Label       string
	Description string
	Line        int
}

// TestSuite is one *TST.m file with its cases.
type TestSuite struct {
	Name     string
	Path     string
	Protocol string // routine hosting start/report (STDASSERT, TESTRUN, …)
	Cases    []TestCase
}

var (
	reSuiteName = regexp.MustCompile(`^[A-Z][A-Z0-9]*TST$`)
	reTestLabel = regexp.MustCompile(`^t[A-Z][A-Za-z0-9]*$`)
	reTestDesc  = regexp.MustCompile(`;@TEST\s+"([^"]*)"`)
	// `do start^XYZ(.pass,.fail)` — abbreviated `d`/case-insensitive `do`; the
	// capture is the routine hosting the start/report protocol.
	reProtocol = regexp.MustCompile(`\b[Dd][Oo]?\s+start\^([A-Z][A-Z0-9]*)\s*\(\s*\.pass\s*,\s*\.fail\s*\)`)
)

// IsSuiteFile reports whether path names a test suite by convention: stem
// matches [A-Z][A-Z0-9]*TST with an M extension (.m on YDB, .mac/.int on IRIS).
func IsSuiteFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m", ".mac", ".int":
	default:
		return false
	}
	stem := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return reSuiteName.MatchString(stem)
}

// DetectProtocol returns the routine hosting start/report for src (the first
// `do start^XYZ(.pass,.fail)`), defaulting to TESTRUN for legacy suites.
func DetectProtocol(src []byte) string {
	if m := reProtocol.FindSubmatch(src); m != nil {
		return string(m[1])
	}
	return "TESTRUN"
}

// FindCases returns the test labels in src: a label qualifies when it matches
// t<UpperCase> and has formals containing both `pass` and `fail`. The first
// label (the routine entry / orchestrator) is never a test.
func FindCases(p *parse.Parser, suiteName string, src []byte) ([]TestCase, error) {
	tree, err := p.Parse(context.Background(), src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()

	var cases []TestCase
	root := tree.RootNode()
	seenFirstLabel := false
	for i := uint32(0); i < root.NamedChildCount(); i++ {
		line := root.NamedChild(i)
		if line.Type() != "line" {
			continue
		}
		label, ok := childOfType(line, "label")
		if !ok {
			continue
		}
		if !seenFirstLabel {
			seenFirstLabel = true // routine entry label is never a test
			continue
		}
		name := string(label.Text())
		if !reTestLabel.MatchString(name) {
			continue
		}
		formals, ok := childOfType(line, "formals")
		if !ok || !hasPassFail(formals) {
			continue
		}
		cases = append(cases, TestCase{
			Suite:       suiteName,
			Label:       name,
			Description: description(line),
			Line:        int(label.StartPoint().Row) + 1,
		})
	}
	return cases, nil
}

// Discover walks paths and returns the suites in name order. Directories are
// scanned recursively for suite-named files; explicit file args are trusted
// (parsed even if the name doesn't match).
func Discover(p *parse.Parser, paths []string) ([]TestSuite, error) {
	seen := map[string]bool{}
	var files []string
	add := func(f string) {
		abs, err := filepath.Abs(f)
		if err != nil {
			abs = f
		}
		if !seen[abs] {
			seen[abs] = true
			files = append(files, f)
		}
	}
	for _, root := range paths {
		info, err := os.Stat(root)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			add(root)
			continue
		}
		err = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				switch d.Name() {
				case ".git", "dist", "vendor", "node_modules":
					return filepath.SkipDir
				}
				return nil
			}
			if IsSuiteFile(path) {
				add(path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	var suites []TestSuite
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(f), filepath.Ext(f))
		cases, err := FindCases(p, name, src)
		if err != nil {
			return nil, err
		}
		suites = append(suites, TestSuite{Name: name, Path: f, Protocol: DetectProtocol(src), Cases: cases})
	}
	sort.Slice(suites, func(i, j int) bool { return suites[i].Name < suites[j].Name })
	return suites, nil
}

func childOfType(n parse.Node, typ string) (parse.Node, bool) {
	for i := uint32(0); i < n.ChildCount(); i++ {
		if c := n.Child(i); c.Type() == typ {
			return c, true
		}
	}
	return parse.Node{}, false
}

func hasPassFail(formals parse.Node) bool {
	var pass, fail bool
	for i := uint32(0); i < formals.ChildCount(); i++ {
		c := formals.Child(i)
		if c.Type() != "identifier" {
			continue
		}
		switch string(c.Text()) {
		case "pass":
			pass = true
		case "fail":
			fail = true
		}
	}
	return pass && fail
}

func description(line parse.Node) string {
	for i := uint32(0); i < line.ChildCount(); i++ {
		c := line.Child(i)
		if c.Type() != "comment" {
			continue
		}
		if m := reTestDesc.FindSubmatch(c.Text()); m != nil {
			return string(m[1])
		}
	}
	return ""
}
