// Package harness is the host-side trigger/render client for the resident
// pure-M harness (design §3.1, spec §9). The harness's contract is its output
// *frame*, not its transport: a deterministic line-delimited envelope of
// verbatim ^STDASSERT per-suite blocks + a verbatim LCOV block + provenance
// tags (§3.2). This package is a thin frame-splitter — it owns NO test/coverage
// parsing. Each suite block is handed to the unchanged mtest.ParseOutput and the
// LCOV block to the unchanged mcov consumers, which is what structurally
// guarantees G4 cross-engine parity (the resident tier and the file-side tier
// speak the exact same dialect).
package harness

import (
	"errors"
	"strconv"
	"strings"
)

// Frame delimiter lines. They never collide with ^STDASSERT / LCOV content,
// which never begin with "##".
const (
	hdrHarness = "##M-HARNESS"   // header: frame=/tier=/engine=/ns=
	hdrSuite   = "##SUITE"       // ##SUITE ^NAME
	hdrEnd     = "##END"         // ##END ^NAME exit=N  (closes a suite)
	hdrLCOV    = "##LCOV"        // ##LCOV … verbatim LCOV tracefile
	hdrTrailer = "##END-HARNESS" // trailer: suites=/pass=/fail= cross-check
)

var (
	// ErrNoFrame means the input had no ##M-HARNESS header — not a frame.
	ErrNoFrame = errors.New("harness: missing ##M-HARNESS header")
	// ErrTruncated means the stream ended before the ##END-HARNESS trailer, or
	// the trailer's suite count disagrees with the blocks parsed — a dropped
	// connection. Partial results are still returned alongside it.
	ErrTruncated = errors.New("harness: truncated frame (missing or mismatched trailer)")
)

// SuiteBlock is one per-suite payload: Name is the suite (the ##SUITE token with
// any leading ^ stripped, so it matches mtest.TestSuite.Name), Body is the
// verbatim ^STDASSERT text between the ##SUITE and ##END lines (fed unchanged to
// mtest.ParseOutput), Exit is the engine exit code from the ##END line.
type SuiteBlock struct {
	Name string
	Body string
	Exit int
}

// FrameMeta carries the header provenance (render label) and the trailer
// cross-check totals.
type FrameMeta struct {
	Frame  int
	Tier   string
	Engine string
	NS     string
	Suites int
	Pass   int
	Fail   int
}

// SplitFrame splits the result frame (§3.2) into per-suite ^STDASSERT blocks,
// the LCOV block (empty when coverage was not requested), and the provenance /
// summary metadata. It is delimiter-scanning only — no test or coverage parsing
// happens here. Unrecognized ## directives are skipped (forward-compat). A
// missing header is ErrNoFrame; a missing/mismatched trailer is ErrTruncated,
// returned alongside whatever was parsed so a caller can still render it.
func SplitFrame(frame string) ([]SuiteBlock, string, FrameMeta, error) {
	var (
		meta       FrameMeta
		suites     []SuiteBlock
		lcov       strings.Builder
		sawHeader  bool
		sawTrailer bool
		inLCOV     bool
		cur        *SuiteBlock
		body       strings.Builder
	)

	flushSuite := func() {
		if cur != nil {
			cur.Body = body.String()
			suites = append(suites, *cur)
			cur = nil
			body.Reset()
		}
	}

	for _, line := range strings.Split(frame, "\n") {
		switch {
		case strings.HasPrefix(line, hdrHarness):
			sawHeader = true
			parseHeader(line, &meta)
		case strings.HasPrefix(line, hdrTrailer):
			flushSuite()
			inLCOV = false
			sawTrailer = true
			parseTrailer(line, &meta)
		case strings.HasPrefix(line, hdrSuite):
			flushSuite()
			inLCOV = false
			name := strings.TrimSpace(strings.TrimPrefix(line, hdrSuite))
			name = strings.TrimPrefix(name, "^")
			cur = &SuiteBlock{Name: name, Exit: -1}
		case strings.HasPrefix(line, hdrEnd) && !strings.HasPrefix(line, hdrTrailer):
			if cur != nil {
				cur.Exit = parseExit(line)
				cur.Body = body.String()
				suites = append(suites, *cur)
				cur = nil
				body.Reset()
			}
		case line == hdrLCOV || strings.HasPrefix(line, hdrLCOV+" "):
			flushSuite()
			inLCOV = true
		case strings.HasPrefix(line, "##"):
			// Unknown directive — skip, never fatal (forward-compat).
		case inLCOV:
			lcov.WriteString(line)
			lcov.WriteByte('\n')
		case cur != nil:
			body.WriteString(line)
			body.WriteByte('\n')
		}
	}
	flushSuite()

	if !sawHeader {
		return suites, lcov.String(), meta, ErrNoFrame
	}
	if !sawTrailer || meta.Suites != len(suites) {
		return suites, lcov.String(), meta, ErrTruncated
	}
	return suites, lcov.String(), meta, nil
}

// parseHeader reads `##M-HARNESS frame=1 tier=integration engine=iris ns=VEHU`.
func parseHeader(line string, m *FrameMeta) {
	for k, v := range kvFields(strings.TrimPrefix(line, hdrHarness)) {
		switch k {
		case "frame":
			m.Frame = atoi(v)
		case "tier":
			m.Tier = v
		case "engine":
			m.Engine = v
		case "ns":
			m.NS = v
		}
	}
}

// parseTrailer reads `##END-HARNESS suites=2 pass=2 fail=1`.
func parseTrailer(line string, m *FrameMeta) {
	for k, v := range kvFields(strings.TrimPrefix(line, hdrTrailer)) {
		switch k {
		case "suites":
			m.Suites = atoi(v)
		case "pass":
			m.Pass = atoi(v)
		case "fail":
			m.Fail = atoi(v)
		}
	}
}

// parseExit reads the exit code from `##END ^NAME exit=N`.
func parseExit(line string) int {
	for k, v := range kvFields(line) {
		if k == "exit" {
			return atoi(v)
		}
	}
	return -1
}

// kvFields splits a run of space-separated key=value tokens into a map.
func kvFields(s string) map[string]string {
	out := map[string]string{}
	for _, tok := range strings.Fields(s) {
		if eq := strings.IndexByte(tok, '='); eq > 0 {
			out[tok[:eq]] = tok[eq+1:]
		}
	}
	return out
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
