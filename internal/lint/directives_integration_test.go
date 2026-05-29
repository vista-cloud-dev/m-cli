package lint_test

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/lint"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// Inline `; m-lint: disable=...` directives drop findings at the Lint choke
// point. M-MOD-037 (subscripted by-reference, default profile) is the probe: it
// fires at line 2 of `do work(.x(1))`.
func TestSuppressionDirectives(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int // findings expected after suppression
	}{
		{
			name: "same-line disable by id suppresses",
			src:  "EN ;\n do work(.x(1)) ; m-lint: disable=M-MOD-037\n quit\n",
			want: 0,
		},
		{
			name: "different rule id on the same line does not suppress",
			src:  "EN ;\n do work(.x(1)) ; m-lint: disable=M-MOD-009\n quit\n",
			want: 1,
		},
		{
			name: "directive on a non-matching line does not suppress",
			src:  "EN ;\n do work(.x(1))\n quit ; m-lint: disable=M-MOD-037\n",
			want: 1,
		},
		{
			name: "disable-next-line suppresses the following line",
			src:  "EN ;\n ; m-lint: disable-next-line=M-MOD-037\n do work(.x(1))\n quit\n",
			want: 0,
		},
		{
			name: "disable-file suppresses regardless of line",
			src:  "EN ; m-lint: disable-file=M-MOD-037\n do work(.x(1))\n quit\n",
			want: 0,
		},
		{
			name: "wildcard suppresses every rule",
			src:  "EN ;\n do work(.x(1)) ; m-lint: disable=*\n quit\n",
			want: 0,
		},
	}

	l := newLinter(t, lint.Profile("default"))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, err := l.Lint(context.Background(), []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			if len(f) != tc.want {
				t.Fatalf("got %d findings %+v, want %d", len(f), f, tc.want)
			}
		})
	}
}

// Internal (linter-about-itself) diagnostics are never suppressible, even by a
// file-wide wildcard — the user must always see when the linter misbehaves.
func TestInternalDiagnosticNotSuppressible(t *testing.T) {
	crash := lint.Rule{
		ID:       "M-INTERNAL-RULE-CRASH",
		Severity: lint.Warning,
		Category: "internal",
		Title:    "synthetic internal diagnostic",
		Tags:     []string{"modern"},
		Inspect: func(_ parse.Node, _ []byte) []lint.Finding {
			return []lint.Finding{{Message: "boom", Line: 2, Col: 2, EndLine: 2, EndCol: 2}}
		},
	}
	l := newLinter(t, []lint.Rule{crash, ruleByRefSubscriptProbe()})

	// disable=* would silence M-MOD-037 but must not silence the internal one.
	src := "EN ;\n do work(.x(1)) ; m-lint: disable=*\n quit\n"
	f, err := l.Lint(context.Background(), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if len(f) != 1 || f[0].Rule != "M-INTERNAL-RULE-CRASH" {
		t.Fatalf("got %+v, want exactly the internal diagnostic (M-MOD-037 suppressed)", f)
	}
}

// ruleByRefSubscriptProbe rebuilds the M-MOD-037 rule for the internal-guard
// test without reaching into the lint package's unexported registry.
func ruleByRefSubscriptProbe() lint.Rule {
	return lint.Rule{
		ID:       "M-MOD-037",
		Severity: lint.Error,
		Category: "portability",
		Title:    "Subscripted by-reference parameter is invalid YottaDB/GT.M syntax",
		Tags:     []string{"modern"},
		Query:    "(by_reference (subscripts)) @ref",
		OnMatch:  func(parse.Match, []byte) (string, bool) { return "by-ref subscript", true },
	}
}
