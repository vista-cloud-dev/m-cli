package lint_test

import (
	"context"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/lint"
)

func countRule(fs []lint.Finding, id string) int {
	n := 0
	for _, f := range fs {
		if f.Rule == id {
			n++
		}
	}
	return n
}

func lintAll(t *testing.T, src string) []lint.Finding {
	t.Helper()
	l := newLinter(t, lint.All())
	fs, err := l.Lint(context.Background(), []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestLineLength(t *testing.T) {
	long := "EN ;" + strings.Repeat("x", 210) + "\n quit\n"
	if got := countRule(lintAll(t, long), "M-MOD-001"); got != 1 {
		t.Errorf("M-MOD-001 on a 214-col line: got %d, want 1", got)
	}
	if got := countRule(lintAll(t, "EN ;\n quit\n"), "M-MOD-001"); got != 0 {
		t.Errorf("M-MOD-001 on short lines: got %d, want 0", got)
	}
}

func TestArgumentCount(t *testing.T) {
	over := "EN(a1,a2,a3,a4,a5,a6,a7,a8) ; eight args\n quit\n"
	if got := countRule(lintAll(t, over), "M-MOD-008"); got != 1 {
		t.Errorf("M-MOD-008 on 8 formals: got %d, want 1", got)
	}
	ok := "EN(a1,a2,a3) ; three args\n quit\n"
	if got := countRule(lintAll(t, ok), "M-MOD-008"); got != 0 {
		t.Errorf("M-MOD-008 on 3 formals: got %d, want 0", got)
	}
}

func TestCommandsPerLine(t *testing.T) {
	over := "EN ;\n s a=1 s b=2 s c=3 s d=4\n"
	if got := countRule(lintAll(t, over), "M-MOD-009"); got != 1 {
		t.Errorf("M-MOD-009 on 4 commands/line: got %d, want 1", got)
	}
	ok := "EN ;\n s a=1 s b=2\n"
	if got := countRule(lintAll(t, ok), "M-MOD-009"); got != 0 {
		t.Errorf("M-MOD-009 on 2 commands/line: got %d, want 0", got)
	}
}

func TestDotBlockNesting(t *testing.T) {
	// Seven dot levels; the deepest line exceeds the limit of 5.
	deep := "EN ;\n d\n . d\n . . d\n . . . d\n . . . . d\n . . . . . d\n . . . . . . w 1\n"
	if got := countRule(lintAll(t, deep), "M-MOD-007"); got == 0 {
		t.Errorf("M-MOD-007 expected on depth-6 dot block; got 0\nsrc:\n%s", deep)
	}
	shallow := "EN ;\n d\n . w 1\n"
	if got := countRule(lintAll(t, shallow), "M-MOD-007"); got != 0 {
		t.Errorf("M-MOD-007 on depth-1 dot block: got %d, want 0", got)
	}
}

// M-MOD-038 — a C-style `\"` quote-escape inside a string literal. In M a
// double-quote is escaped by doubling it (""), not with a backslash; `\"`
// terminates the string and silently corrupts the routine.
func TestCStyleQuoteEscape(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want int
	}{
		// The real v-stdlib FU-5 5B.1 repro: an assertion description with a
		// C-style escaped quote. The string ends early at the first `\"`, which
		// is followed by a word char (CAPI) — the mis-escape signal.
		{"assert-desc-repro", "EN ;\n do eq^STDASSERT(.pass,.fail,x,\"y\",\"rpc name from XWB(2,\\\"CAPI\\\")\")\n", 1},
		{"single-escaped-quote", "EN ;\n set x=\"name=\\\"value\"\n", 1},
		// Negatives — none of these is a C-style escape.
		{"doubled-quote", "EN ;\n set x=\"he said \"\"hi\"\"\"\n", 0},
		{"backslash-not-before-quote", "EN ;\n set x=\"a\\b\\c\"\n", 0},
		{"windows-path", "EN ;\n set x=\"C:\\tmp\"\n", 0},
		// A string whose content legitimately ends in a backslash: the `"` is the
		// real terminator (followed by EOL/delimiter), not a mistaken escape.
		{"trailing-backslash-legit", "EN ;\n set x=\"C:\\\"\n", 0},
		// A `\"` that appears only inside a trailing `;` comment is not in code.
		{"escape-in-comment", "EN ;\n set x=1 ; see XWB(2,\\\"CAPI\\\")\n", 0},
		// Integer-divide operator `\` in code (outside any string) is not flagged.
		{"integer-divide-operator", "EN ;\n set x=7\\2\n", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fs := lintAll(t, tc.src)
			if got := countRule(fs, "M-MOD-038"); got != tc.want {
				t.Errorf("M-MOD-038 count = %d, want %d\nsrc: %q\nfindings: %+v", got, tc.want, tc.src, fs)
			}
			if tc.want > 0 {
				for _, f := range fs {
					if f.Rule == "M-MOD-038" && f.Severity != lint.Error {
						t.Errorf("M-MOD-038 severity = %q, want error", f.Severity)
					}
				}
			}
		})
	}
}

// M-MOD-038 fires at error severity and is honored by inline disable directives
// (the centralized suppression choke point) like every other rule.
func TestCStyleQuoteEscapeDisable(t *testing.T) {
	src := "EN ;\n set x=\"name=\\\"value\" ; m-lint: disable=M-MOD-038\n"
	if got := countRule(lintAll(t, src), "M-MOD-038"); got != 0 {
		t.Errorf("disable=M-MOD-038 should suppress; got %d findings", got)
	}
}

// A `\"` mis-escape is wrong in every dialect, so M-MOD-038 carries both the
// modern and vista tags and must fire under both dialect knobs (and stay out of
// pedantic so it lives in default). Pin that intent: a refactor that drops a tag
// would otherwise pass silently.
func TestCStyleQuoteEscapeDialectCoverage(t *testing.T) {
	src := "EN ;\n set x=\"name=\\\"value\"\n"
	for _, profile := range []string{"default", "modern", "pythonic", "vista", "all"} {
		l := newLinter(t, lint.Profile(profile))
		fs, err := l.Lint(context.Background(), []byte(src))
		if err != nil {
			t.Fatal(err)
		}
		if got := countRule(fs, "M-MOD-038"); got != 1 {
			t.Errorf("profile %q: M-MOD-038 count = %d, want 1", profile, got)
		}
	}
}

// The default profile is modern minus pedantic: the metric/portability rules
// are in; the pedantic style nitpicks (M-MOD-009, M-STY-001) are not.
func TestDefaultProfileMembership(t *testing.T) {
	ids := map[string]bool{}
	for _, r := range lint.Profile("default") {
		ids[r.ID] = true
	}
	for _, want := range []string{"M-MOD-001", "M-MOD-007", "M-MOD-008", "M-MOD-037", "M-MOD-036"} {
		if !ids[want] {
			t.Errorf("default profile missing %s", want)
		}
	}
	for _, no := range []string{"M-MOD-009", "M-STY-001"} {
		if ids[no] {
			t.Errorf("default profile should exclude pedantic rule %s", no)
		}
	}
	// modern includes the pedantic ones; all >= modern.
	if len(lint.Profile("modern")) <= len(lint.Profile("default")) {
		t.Error("modern profile should be a superset of default")
	}
}
