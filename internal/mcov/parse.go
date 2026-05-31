package mcov

import (
	"strconv"
	"strings"
)

// ParseLCOV reads an LCOV tracefile into a Result. It is the inverse of LCOV:
// a coverage payload that arrives as text (the resident harness ##LCOV frame
// block, §3.2) parses back here so it feeds the same ByFile / Percent / gutter
// consumers a host-side mcov.Run produces — the parity contract. Only SF: and
// DA: lines carry data; TN:/LF:/LH:/end_of_record and anything else are ignored
// (so output of this package's own LCOV, with its TN:/LF:/LH: lines, round-trips
// and a foreign producer's extra records do not break the parse).
func ParseLCOV(text string) (Result, error) {
	var r Result
	path := ""
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimRight(raw, "\r")
		switch {
		case strings.HasPrefix(line, "SF:"):
			path = line[len("SF:"):]
		case strings.HasPrefix(line, "DA:") && path != "":
			rest := line[len("DA:"):]
			comma := strings.IndexByte(rest, ',')
			if comma < 0 {
				continue
			}
			ln, err1 := strconv.Atoi(strings.TrimSpace(rest[:comma]))
			hits, err2 := strconv.Atoi(strings.TrimSpace(rest[comma+1:]))
			if err1 != nil || err2 != nil {
				continue
			}
			r.Lines = append(r.Lines, LineCov{Path: path, Line: ln, Hits: hits})
		case line == "end_of_record":
			path = ""
		}
	}
	return r, nil
}
