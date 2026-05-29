package lint

import "testing"

// parseDirectives + isSuppressed: each comment form, the wildcard, multi-rule
// lists, whitespace tolerance, trailing-comment no-bleed, case-sensitivity, and
// disable-next-line targeting the line after the comment.
func TestParseDirectives(t *testing.T) {
	cases := []struct {
		name string
		src  string
		// probes: each is (line, rule, wantSuppressed).
		probes []struct {
			line int
			rule string
			want bool
		}
	}{
		{
			name: "same-line disable",
			src:  "EN ;\n s x=1 ; m-lint: disable=M-STY-001\n w y\n",
			probes: []struct {
				line int
				rule string
				want bool
			}{
				{2, "M-STY-001", true},  // on the comment line
				{3, "M-STY-001", false}, // a later line is unaffected
				{2, "M-MOD-009", false}, // a different rule on the same line
			},
		},
		{
			name: "disable-next-line targets the following line",
			src:  "EN ;\n ; m-lint: disable-next-line=M-MOD-024\n w undef\n",
			probes: []struct {
				line int
				rule string
				want bool
			}{
				{2, "M-MOD-024", false}, // the comment line itself
				{3, "M-MOD-024", true},  // the line after
				{4, "M-MOD-024", false},
			},
		},
		{
			name: "disable-file suppresses every line",
			src:  "EN ; m-lint: disable-file=M-MOD-025\n s x=1\n w y\n",
			probes: []struct {
				line int
				rule string
				want bool
			}{
				{1, "M-MOD-025", true},
				{3, "M-MOD-025", true},
				{99, "M-MOD-025", true},
				{2, "M-MOD-024", false}, // a different rule is still live
			},
		},
		{
			name: "wildcard matches every rule",
			src:  "EN ;\n s x=1 ; m-lint: disable=*\n",
			probes: []struct {
				line int
				rule string
				want bool
			}{
				{2, "M-MOD-025", true},
				{2, "ANYTHING", true},
				{3, "M-MOD-025", false},
			},
		},
		{
			name: "wildcard disable-file",
			src:  "EN ; m-lint: disable-file=*\n s x=1\n",
			probes: []struct {
				line int
				rule string
				want bool
			}{
				{1, "M-MOD-009", true},
				{2, "M-MOD-036", true},
			},
		},
		{
			name: "multi-rule list",
			src:  "EN ;\n s x=1 ; m-lint: disable=M-MOD-024,M-MOD-025\n",
			probes: []struct {
				line int
				rule string
				want bool
			}{
				{2, "M-MOD-024", true},
				{2, "M-MOD-025", true},
				{2, "M-MOD-026", false},
			},
		},
		{
			name: "whitespace tolerance around colon and equals",
			src:  "EN ;\n s x=1 ;m-lint:disable=M-MOD-024\n w y ;  m-lint  :  disable  =  M-MOD-025\n",
			probes: []struct {
				line int
				rule string
				want bool
			}{
				{2, "M-MOD-024", true},
				{3, "M-MOD-025", true},
			},
		},
		{
			name: "trailing comment does not bleed into the rule list",
			src:  "EN ;\n s x=1 ; m-lint: disable=M-MOD-024 because legacy code\n",
			probes: []struct {
				line int
				rule string
				want bool
			}{
				{2, "M-MOD-024", true},
				{2, "because", false}, // the prose after the space is not a rule id
				{2, "legacy", false},
			},
		},
		{
			name: "rule ids are case sensitive",
			src:  "EN ;\n s x=1 ; m-lint: disable=m-mod-036\n",
			probes: []struct {
				line int
				rule string
				want bool
			}{
				{2, "M-MOD-036", false}, // lowercase directive must not match
				{2, "m-mod-036", true},
			},
		},
		{
			name: "empty value list is ignored",
			src:  "EN ;\n s x=1 ; m-lint: disable=\n",
			probes: []struct {
				line int
				rule string
				want bool
			}{
				{2, "M-MOD-024", false},
			},
		},
		{
			name: "multiple directives on one line accumulate",
			src:  "EN ;\n s x ; m-lint: disable=M-MOD-024 ; m-lint: disable=M-MOD-025\n",
			probes: []struct {
				line int
				rule string
				want bool
			}{
				{2, "M-MOD-024", true},
				{2, "M-MOD-025", true},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sup := parseDirectives([]byte(tc.src))
			for _, p := range tc.probes {
				if got := sup.isSuppressed(p.line, p.rule); got != p.want {
					t.Errorf("isSuppressed(%d, %q) = %v, want %v", p.line, p.rule, got, p.want)
				}
			}
		})
	}
}

func TestParseDirectivesEmpty(t *testing.T) {
	sup := parseDirectives([]byte("EN ;\n s x=1\n w y\n"))
	if !sup.empty() {
		t.Errorf("expected empty suppressions for a directive-free source")
	}
	if sup.isSuppressed(1, "M-MOD-024") {
		t.Errorf("nothing should be suppressed without directives")
	}
}
