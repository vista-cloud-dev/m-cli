package lint

import (
	"regexp"
	"strings"
)

// Inline lint-suppression directives — the ruff / ESLint pattern: a comment in
// the source tells the linter to ignore specific rule findings for a scope.
// Three forms, each parsed from any `; m-lint: ...` comment:
//
//	; m-lint: disable=RULE[,RULE...]            suppress on the same line
//	; m-lint: disable-next-line=RULE[,RULE...]  suppress on the line after
//	; m-lint: disable-file=RULE[,RULE...]        suppress file-wide
//
// The wildcard `*` matches every rule. Whitespace around `:`/`=` is forgiving;
// the value list runs to the next whitespace or `;` so a trailing inline
// comment doesn't bleed in. Rule IDs are case-sensitive (M-MOD-036, not lower).

// directiveRE captures the kind (disable / disable-next-line / disable-file)
// and the comma-separated rule-id list.
var directiveRE = regexp.MustCompile(`;\s*m-lint\s*:\s*(disable(?:-next-line|-file)?)\s*=\s*([^\s;]+)`)

// suppressions is the resolved set of (line, rule) suppressions for one file.
type suppressions struct {
	fileDisable map[string]bool         // rule IDs disabled file-wide; "*" = all
	lineDisable map[int]map[string]bool // 1-based line -> rule IDs
}

func (s suppressions) empty() bool {
	return len(s.fileDisable) == 0 && len(s.lineDisable) == 0
}

// isSuppressed reports whether a directive in the file silences the given
// (line, rule) finding.
func (s suppressions) isSuppressed(line int, rule string) bool {
	if s.fileDisable["*"] || s.fileDisable[rule] {
		return true
	}
	rules := s.lineDisable[line]
	return rules["*"] || rules[rule]
}

// parseDirectives walks the source for `; m-lint: ...` comments and resolves
// them. Multiple directives on one line accumulate; malformed forms are
// silently ignored — a typo in a comment must never crash the lint pass.
func parseDirectives(src []byte) suppressions {
	s := suppressions{fileDisable: map[string]bool{}, lineDisable: map[int]map[string]bool{}}
	for i, line := range strings.Split(string(src), "\n") {
		lineNo := i + 1
		for _, m := range directiveRE.FindAllStringSubmatch(line, -1) {
			kind, ids := m[1], splitIDs(m[2])
			if len(ids) == 0 {
				continue
			}
			switch kind {
			case "disable-file":
				for _, id := range ids {
					s.fileDisable[id] = true
				}
			case "disable-next-line":
				s.addLine(lineNo+1, ids)
			default: // "disable"
				s.addLine(lineNo, ids)
			}
		}
	}
	return s
}

func (s suppressions) addLine(line int, ids []string) {
	set := s.lineDisable[line]
	if set == nil {
		set = map[string]bool{}
		s.lineDisable[line] = set
	}
	for _, id := range ids {
		set[id] = true
	}
}

func splitIDs(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
