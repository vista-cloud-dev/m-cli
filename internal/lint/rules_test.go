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
