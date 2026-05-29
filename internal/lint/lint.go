// Package lint is m-cli's M source linter. Rules come in two shapes, both over
// the m-parse tree:
//
//   - query rules carry a tree-sitter query + an OnMatch that turns each match
//     into a finding (good for pattern detection, e.g. M-MOD-037);
//   - walk rules carry an Inspect that traverses the tree / scans the source
//     and returns findings (good for metrics + structure, e.g. line length,
//     nesting depth, argument count).
//
// Rules are tagged; profiles select rules by tag (spec §3.1). This is the
// engine + a growing rule set — the dataflow/taint rules (M-MOD-011..036) need
// the flow-analysis infra, a later port.
package lint

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// internalRulePrefix marks diagnostics the linter emits about itself (a rule
// crash, a parse failure). These are never suppressible — the user always wants
// to know when the linter misbehaves. No such rule exists yet (parse errors
// surface as Lint's error return), but the guard keeps the contract if one is
// added, mirroring the Python tool's M-INTERNAL-RULE-CRASH carve-out.
const internalRulePrefix = "M-INTERNAL-"

// Severity ranks a finding.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Style   Severity = "style"
	Info    Severity = "info"
)

// Finding is one lint result (1-based positions; End* mark the offending span).
type Finding struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Line     int      `json:"line"`
	Col      int      `json:"col"`
	EndLine  int      `json:"endLine"`
	EndCol   int      `json:"endCol"`
}

// Rule is a lint rule. Exactly one of Query (with OnMatch) or Inspect is set.
type Rule struct {
	ID       string
	Severity Severity
	Category string
	Title    string
	Tags     []string

	// Query rule: Query is tree-sitter source; OnMatch is called per match and
	// returns the finding message + whether to emit. The position is the start
	// of the match's first capture.
	Query   string
	OnMatch func(m parse.Match, src []byte) (string, bool)

	// Walk rule: Inspect traverses root / scans src and returns findings with
	// Line/Col/End* + Message set; the engine stamps Rule + Severity.
	Inspect func(root parse.Node, src []byte) []Finding
}

func (r Rule) hasTag(tag string) bool {
	for _, t := range r.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// Linter compiles the query rules' queries once and runs the rule set over
// many sources.
type Linter struct {
	p        *parse.Parser
	rules    []Rule
	compiled map[string]*parse.Query
}

// NewLinter compiles every query rule's query against the grammar.
func NewLinter(p *parse.Parser, rules []Rule) (*Linter, error) {
	l := &Linter{p: p, rules: rules, compiled: map[string]*parse.Query{}}
	for _, r := range rules {
		if r.Query == "" {
			continue
		}
		q, err := p.NewQuery(r.Query)
		if err != nil {
			l.Close()
			return nil, fmt.Errorf("lint: rule %s: %w", r.ID, err)
		}
		l.compiled[r.ID] = q
	}
	return l, nil
}

// Close frees the compiled queries.
func (l *Linter) Close() {
	for _, q := range l.compiled {
		if q != nil {
			q.Close()
		}
	}
	l.compiled = nil
}

// Lint parses src and returns the findings from every rule, sorted by position.
func (l *Linter) Lint(ctx context.Context, src []byte) ([]Finding, error) {
	tree, err := l.p.Parse(ctx, src)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.RootNode()

	var findings []Finding
	for _, r := range l.rules {
		if r.Query != "" {
			for _, m := range l.compiled[r.ID].Matches(root) {
				if len(m.Captures) == 0 {
					continue
				}
				msg, ok := r.OnMatch(m, src)
				if !ok {
					continue
				}
				node := m.Captures[0].Node
				start, end := node.StartPoint(), node.EndPoint()
				findings = append(findings, Finding{
					Rule: r.ID, Severity: r.Severity, Message: msg,
					Line: int(start.Row) + 1, Col: int(start.Column) + 1,
					EndLine: int(end.Row) + 1, EndCol: int(end.Column) + 1,
				})
			}
			continue
		}
		for _, f := range r.Inspect(root, src) {
			f.Rule = r.ID
			f.Severity = r.Severity
			findings = append(findings, f)
		}
	}
	sort.SliceStable(findings, func(a, b int) bool {
		if findings[a].Line != findings[b].Line {
			return findings[a].Line < findings[b].Line
		}
		if findings[a].Col != findings[b].Col {
			return findings[a].Col < findings[b].Col
		}
		return findings[a].Rule < findings[b].Rule
	})

	// Drop findings silenced by inline `; m-lint: disable=...` directives. This
	// is the single choke point, so every rule is suppressible uniformly —
	// except internal (linter-about-itself) diagnostics, which are never hidden.
	if len(findings) > 0 {
		if sup := parseDirectives(src); !sup.empty() {
			kept := findings[:0]
			for _, f := range findings {
				if strings.HasPrefix(f.Rule, internalRulePrefix) || !sup.isSuppressed(f.Line, f.Rule) {
					kept = append(kept, f)
				}
			}
			findings = kept
		}
	}
	return findings, nil
}
