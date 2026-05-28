// Package mtest is m-cli's test runner (spec §3.2): it discovers *TST.m suites,
// runs them through the Engine adapter, and parses the pure-M ^STDASSERT /
// TESTRUN output. The assertion library and suites are unchanged M that runs on
// both YottaDB and IRIS; only the host-side discovery + parsing live here.
package mtest

import (
	"regexp"
	"strconv"
	"strings"
)

// Outcome is a single assertion's result.
type Outcome string

const (
	Pass Outcome = "pass"
	Fail Outcome = "fail"
)

// Assertion is one "  PASS/FAIL  <desc>" line (with optional expected/actual).
type Assertion struct {
	Outcome     Outcome
	Description string
	Expected    string
	Actual      string
}

// Summary is the parsed result of running a suite.
type Summary struct {
	Passed     int
	Failed     int
	Total      int
	OK         bool
	Assertions []Assertion
}

var (
	reResults  = regexp.MustCompile(`(?m)^Results:\s+(\d+)\s+tests\s+(\d+)\s+passed\s+(\d+)\s+failed`)
	reFailed   = regexp.MustCompile(`(?m)\d+\s+test\(s\)\s+FAILED`)
	rePassed   = regexp.MustCompile(`(?m)All tests passed\.`)
	rePassLine = regexp.MustCompile(`^  PASS  (.+)$`)
	reFailLine = regexp.MustCompile(`^  FAIL  (.+)$`)
	reExpected = regexp.MustCompile(`^         expected:\s*(.*)$`)
	reActual   = regexp.MustCompile(`^         actual:\s*(.*)$`)
)

// ParseOutput parses ^STDASSERT / TESTRUN output into a Summary. The contract
// (STDASSERT.m): per-assertion "  PASS/FAIL  <desc>" lines, a
// "Results: <total> tests  <p> passed  <f> failed" summary, then either
// "All tests passed." or "<n> test(s) FAILED.".
func ParseOutput(stdout string) Summary {
	var s Summary
	if m := reResults.FindStringSubmatch(stdout); m != nil {
		s.Total = atoi(m[1])
		s.Passed = atoi(m[2])
		s.Failed = atoi(m[3])
	}
	switch {
	case reFailed.MatchString(stdout):
		s.OK = false
	case rePassed.MatchString(stdout):
		s.OK = true
	default:
		s.OK = s.Total > 0 && s.Failed == 0
	}

	lines := splitLines(stdout)
	for i := 0; i < len(lines); i++ {
		if m := rePassLine.FindStringSubmatch(lines[i]); m != nil {
			s.Assertions = append(s.Assertions, Assertion{Outcome: Pass, Description: rtrim(m[1])})
			continue
		}
		if m := reFailLine.FindStringSubmatch(lines[i]); m != nil {
			a := Assertion{Outcome: Fail, Description: rtrim(m[1])}
			if i+1 < len(lines) {
				if em := reExpected.FindStringSubmatch(lines[i+1]); em != nil {
					a.Expected = rtrim(em[1])
					i++
				}
			}
			if i+1 < len(lines) {
				if am := reActual.FindStringSubmatch(lines[i+1]); am != nil {
					a.Actual = rtrim(am[1])
					i++
				}
			}
			s.Assertions = append(s.Assertions, a)
		}
	}
	return s
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func splitLines(s string) []string { return strings.Split(s, "\n") }

func rtrim(s string) string { return strings.TrimRight(s, " \t\r") }
