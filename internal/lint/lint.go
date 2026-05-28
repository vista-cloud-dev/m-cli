// Package lint is m-cli's M source linter. Rules are query-driven: each rule
// carries a tree-sitter query (run over the m-parse tree) plus a Check that
// turns each match into an optional finding. This is the engine + a starter
// rule set; full M-MOD parity and the 8 profiles (spec §3.1) follow, gated by
// the corpus (G1).
package lint

import (
	"context"
	"fmt"
	"sort"

	"github.com/vista-cloud-dev/m-parse/parse"
)

// Severity ranks a finding. (The Python tool's two-axis severity, simplified to
// the levels the starter rules need.)
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
	Style   Severity = "style"
	Info    Severity = "info"
)

// Finding is one lint result, with a 1-based line/column for display.
type Finding struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
	Line     int      `json:"line"`
	Col      int      `json:"col"`
}

// Rule is a query-driven lint rule. Query is tree-sitter query source; Check is
// called once per match and returns the finding message plus whether to emit.
// The finding's position is the start of the match's first capture.
type Rule struct {
	ID       string
	Severity Severity
	Profiles []string
	Query    string
	Doc      string
	Check    func(m parse.Match, src []byte) (string, bool)
}

// Linter compiles a rule set's queries once and runs them over many sources.
type Linter struct {
	p        *parse.Parser
	rules    []Rule
	compiled []*parse.Query // parallel to rules
}

// NewLinter compiles every rule's query against the grammar.
func NewLinter(p *parse.Parser, rules []Rule) (*Linter, error) {
	l := &Linter{p: p, rules: rules}
	for _, r := range rules {
		q, err := p.NewQuery(r.Query)
		if err != nil {
			l.Close()
			return nil, fmt.Errorf("lint: rule %s: %w", r.ID, err)
		}
		l.compiled = append(l.compiled, q)
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
	for i, r := range l.rules {
		for _, m := range l.compiled[i].Matches(root) {
			if len(m.Captures) == 0 {
				continue
			}
			msg, ok := r.Check(m, src)
			if !ok {
				continue
			}
			pos := m.Captures[0].Node.StartPoint()
			findings = append(findings, Finding{
				Rule: r.ID, Severity: r.Severity, Message: msg,
				Line: int(pos.Row) + 1, Col: int(pos.Column) + 1,
			})
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
	return findings, nil
}
