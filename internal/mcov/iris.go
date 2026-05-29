package mcov

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/vista-cloud-dev/m-cli/internal/engine"
)

// IRIS line coverage uses the %Monitor.System.LineByLine monitor (^%MONLBL),
// which reports ABSOLUTE 1-based line numbers — no label-relative offset
// reconciliation (spec §8). Crucially the data must be read BEFORE Stop(),
// which clears it; the script retrieves per-line counts via the Result query
// and emits `MLINE:<routine>:<line>:<count>` for the host to parse.

// buildMonScript composes the IRIS direct-mode coverage script: start the
// line monitor over the production routines, run the suites, dump per-line
// counts (before Stop), then stop.
func buildMonScript(routineNames, suiteEntries []string) string {
	quoted := make([]string, len(routineNames))
	for i, r := range routineNames {
		quoted[i] = `"` + r + `"`
	}
	var b strings.Builder
	b.WriteString(`set sc=##class(%Monitor.System.LineByLine).Start($lb(` +
		strings.Join(quoted, ",") + `),$lb("RtnLine"),$lb($job))` + "\n")
	for _, e := range suiteEntries {
		b.WriteString("do ^" + e + "\n")
	}
	for _, r := range routineNames {
		b.WriteString(`set rs=##class(%ResultSet).%New("%Monitor.System.LineByLine:Result") ` +
			`do rs.Execute("` + r + `") set ln=0 ` +
			`while rs.Next() { set ln=ln+1 write "MLINE:` + r + `:",ln,":",$listget(rs.GetData(1),1),! } ` +
			`kill rs` + "\n")
	}
	b.WriteString(`do ##class(%Monitor.System.LineByLine).Stop()` + "\n")
	b.WriteString("halt\n")
	return b.String()
}

type monKey struct {
	routine string
	line    int
}

var reMon = regexp.MustCompile(`^MLINE:([^:]+):(\d+):(\d+)$`)

// parseMon parses the MLINE:<routine>:<line>:<count> stream into per-line hits
// keyed by (UPPER routine, absolute line).
func parseMon(stdout string) map[monKey]int {
	out := map[monKey]int{}
	for _, raw := range strings.Split(stdout, "\n") {
		m := reMon.FindStringSubmatch(strings.TrimSpace(raw))
		if m == nil {
			continue
		}
		line, err1 := strconv.Atoi(m[2])
		count, err2 := strconv.Atoi(m[3])
		if err1 != nil || err2 != nil {
			continue
		}
		out[monKey{strings.ToUpper(m[1]), line}] = count
	}
	return out
}

// runIris runs the suites under the IRIS line monitor and joins the absolute
// per-line counts back to the executable lines.
func runIris(ctx context.Context, eng engine.Engine, execs []ExecLine, routineNames, suiteEntries []string) (Result, error) {
	res, err := eng.RunScript(ctx, buildMonScript(routineNames, suiteEntries))
	if err != nil {
		return Result{}, err
	}
	hits := parseMon(res.Stdout)
	lines := make([]LineCov, 0, len(execs))
	for _, ex := range execs {
		lines = append(lines, LineCov{
			Routine: ex.Routine, Label: ex.Label, Path: ex.Path, Line: ex.Line,
			Hits: hits[monKey{strings.ToUpper(ex.Routine), ex.Line}],
		})
	}
	return Result{Lines: lines, Stdout: res.Stdout}, nil
}
