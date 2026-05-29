// Package mcov is m-cli's coverage runner (spec §3.1/§8). On YottaDB it uses
// built-in line tracing: `view "TRACE":1:"^ycov":""` makes every executed line
// increment ^ycov(routine, LABEL, offset) where offset is from the owning
// label's declaration line. The host enumerates executable lines from the parse
// tree (the denominator), runs the suites under trace, then joins the ^ycov
// dump (the numerator) back to absolute lines for LCOV.
//
// (IRIS coverage via ^%MONLBL — absolute lines, no offset reconciliation — is
// the parity follow-up; this package implements the YDB path.)
package mcov

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// ExecLine is one executable source line, with the YDB trace offset (the line's
// distance from its owning label's declaration line).
type ExecLine struct {
	Routine string
	Label   string
	Path    string
	Line    int
	Offset  int
}

// LineCov is an executable line plus how many times it ran.
type LineCov struct {
	Routine string
	Label   string
	Path    string
	Line    int
	Hits    int
}

// Result is the aggregate coverage outcome.
type Result struct {
	Lines  []LineCov
	Stdout string
}

// Total is the number of executable lines (the denominator).
func (r Result) Total() int { return len(r.Lines) }

// Covered is the number of executable lines that ran at least once.
func (r Result) Covered() int {
	n := 0
	for _, l := range r.Lines {
		if l.Hits > 0 {
			n++
		}
	}
	return n
}

// Percent is line coverage as a percentage (0 when there are no lines).
func (r Result) Percent() float64 {
	if r.Total() == 0 {
		return 0
	}
	return 100 * float64(r.Covered()) / float64(r.Total())
}

// BuildScript composes the YDB direct-mode trace script: reset ^ycov, enable
// line tracing, run every suite entry, disable tracing, dump ^ycov, halt.
func BuildScript(suiteEntries []string) string {
	lines := []string{`kill ^ycov`, `view "TRACE":1:"^ycov":""`}
	for _, e := range suiteEntries {
		lines = append(lines, "do ^"+e)
	}
	lines = append(lines, `view "TRACE":0:"^ycov":""`, `zwrite ^ycov`, `halt`)
	return strings.Join(lines, "\n") + "\n"
}

// ^ycov("routine","LABEL",offset)="<hits>:…". Two-subscript entries (label
// totals) and *RUN/*CHILDREN summaries don't match (no third subscript).
var reYcov = regexp.MustCompile(`^\^ycov\("([^"]+)","([^"]+)",(\d+)\)="(\d+):`)

type ycovKey struct {
	routine string
	label   string
	offset  int
}

// parseYcov parses a `zwrite ^ycov` dump into per-line hit counts keyed by
// (UPPER routine, UPPER label, offset).
func parseYcov(stdout string) map[ycovKey]int {
	out := map[ycovKey]int{}
	for _, raw := range strings.Split(stdout, "\n") {
		m := reYcov.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			continue
		}
		offset, err1 := strconv.Atoi(m[3])
		count, err2 := strconv.Atoi(m[4])
		if err1 != nil || err2 != nil {
			continue
		}
		out[ycovKey{strings.ToUpper(m[1]), strings.ToUpper(m[2]), offset}] = count
	}
	return out
}

// Run enumerates executable lines in routinePaths, runs the suites under the
// engine's line tracer, and joins per-line hits back to those lines. YDB uses
// view "TRACE" → ^ycov (label-relative offsets); IRIS uses the LineByLine
// monitor (absolute lines).
func Run(ctx context.Context, p *parse.Parser, eng engine.Engine, routinePaths, suiteEntries []string) (Result, error) {
	execs, err := DiscoverExecutables(p, routinePaths)
	if err != nil {
		return Result{}, err
	}
	if eng.Kind() == engine.IRIS {
		names := make([]string, 0, len(routinePaths))
		for _, rp := range routinePaths {
			names = append(names, strings.ToUpper(strings.TrimSuffix(filepath.Base(rp), filepath.Ext(rp))))
		}
		return runIris(ctx, eng, execs, names, suiteEntries)
	}
	res, err := eng.RunScript(ctx, BuildScript(suiteEntries))
	if err != nil {
		return Result{}, err
	}
	hits := parseYcov(res.Stdout)
	lines := make([]LineCov, 0, len(execs))
	for _, ex := range execs {
		lines = append(lines, LineCov{
			Routine: ex.Routine, Label: ex.Label, Path: ex.Path, Line: ex.Line,
			Hits: hits[ycovKey{strings.ToUpper(ex.Routine), strings.ToUpper(ex.Label), ex.Offset}],
		})
	}
	return Result{Lines: lines, Stdout: res.Stdout}, nil
}

// LCOV renders the result as an LCOV tracefile (one SF block per source file).
func LCOV(r Result) string {
	byPath := map[string][]LineCov{}
	var order []string
	for _, l := range r.Lines {
		if _, ok := byPath[l.Path]; !ok {
			order = append(order, l.Path)
		}
		byPath[l.Path] = append(byPath[l.Path], l)
	}
	sort.Strings(order)
	var b strings.Builder
	for _, path := range order {
		ls := byPath[path]
		sort.Slice(ls, func(i, j int) bool { return ls[i].Line < ls[j].Line })
		b.WriteString("TN:\n")
		fmt.Fprintf(&b, "SF:%s\n", path)
		hit := 0
		for _, l := range ls {
			fmt.Fprintf(&b, "DA:%d,%d\n", l.Line, l.Hits)
			if l.Hits > 0 {
				hit++
			}
		}
		fmt.Fprintf(&b, "LF:%d\n", len(ls))
		fmt.Fprintf(&b, "LH:%d\n", hit)
		b.WriteString("end_of_record\n")
	}
	return b.String()
}
