package lint_test

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/lint"
)

// lintX lints src under the "all" profile (so every xindex rule runs) with the
// given routine name threaded through, returning the findings.
func lintX(t *testing.T, src, routine string) []lint.Finding {
	t.Helper()
	l := newLinter(t, lint.Profile("all"))
	f, err := l.LintNamed(context.Background(), []byte(src), routine)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	return f
}

func countRuleID(findings []lint.Finding, id string) int {
	n := 0
	for _, f := range findings {
		if f.Rule == id {
			n++
		}
	}
	return n
}

// Each xindex rule fires on a target snippet. Routine name "FOO" matches the
// label, so M-XINDX-017 doesn't fire spuriously on these.
func TestXindexFlagged(t *testing.T) {
	cases := []struct {
		id, src string
	}{
		{"M-XINDX-002", "FOO ;\n ZBOGUS\n"},
		{"M-XINDX-009", "FOO ;\n Q\n W 1\n"},
		{"M-XINDX-013", "FOO ;\n W 1 \n"},
		{"M-XINDX-015", "FOO ;\nBAR ;\nBAR ;\n"},
		{"M-XINDX-018", "FOO ;\n W 1\x07\n"},
		{"M-XINDX-019", "FOO ;\n W \"" + repeat("x", 250) + "\"\n"},
		{"M-XINDX-020", "FOO ;\n VIEW\n"},
		{"M-XINDX-022", "FOO ;\n K (A,B)\n"},
		{"M-XINDX-023", "FOO ;\n K\n"},
		{"M-XINDX-024", "FOO ;\n K ^GBL\n"},
		{"M-XINDX-025", "FOO ;\n BREAK\n"},
		{"M-XINDX-026", "FOO ;\n N (A,B)\n"},
		{"M-XINDX-027", "FOO ;\n W $V(1)\n"},
		{"M-XINDX-028", "FOO ;\n W $ZBOGUS\n"},
		{"M-XINDX-029", "FOO ;\n C 1\n"},
		{"M-XINDX-030", "FOO ;\n D TAG+1\n"},
		{"M-XINDX-031", "FOO ;\n W $ZBITAND(1)\n"},
		{"M-XINDX-032", "FOO ;\n HALT\n"},
		{"M-XINDX-033", "FOO ;\n R X\n"},
		{"M-XINDX-034", "FOO ;\n O 1\n"},
		{"M-XINDX-035", "FOO ;\n W \"" + repeat("y", 20001) + "\"\n"},
		{"M-XINDX-036", "FOO ;\n J ^OTHER\n"},
		{"M-XINDX-041", "FOO ;\n R *X\n"},
		{"M-XINDX-042", "FOO ;\n\n W 1\n"},
		{"M-XINDX-045", "FOO ;\n S ^%GBL=1\n"},
		{"M-XINDX-047", "FOO ;\n write 1\n"},
		{"M-XINDX-051", "FOO ;\n I X>0\n"},
		{"M-XINDX-054", "FOO ;\n W $SYSTEM\n"},
		{"M-XINDX-057", "FOO ;\n S abc=1\n"},
		{"M-XINDX-060", "FOO ;\n L ^X\n"},
		{"M-XINDX-061", "FOO ;\n L ^X\n"},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			f := lintX(t, tc.src, "FOO")
			if countRuleID(f, tc.id) == 0 {
				t.Errorf("%s did not fire on %q; findings=%+v", tc.id, tc.src, f)
			}
		})
	}
}

// M-XINDX-014 — missing in-routine label call (bare-label form works without a
// routine name).
func TestXindex014MissingLabel(t *testing.T) {
	f := lintX(t, "FOO ;\n D NOPE\n", "FOO")
	if countRuleID(f, "M-XINDX-014") != 1 {
		t.Fatalf("want 1 M-XINDX-014, got %+v", f)
	}
	// A defined label is clean.
	g := lintX(t, "FOO ;\n D BAR\nBAR ;\n Q\n", "FOO")
	if countRuleID(g, "M-XINDX-014") != 0 {
		t.Errorf("defined label should be clean, got %+v", g)
	}
}

// M-XINDX-017 — first label != routine name; needs the name; % routines exempt.
func TestXindex017FirstLabel(t *testing.T) {
	if n := countRuleID(lintX(t, "BAR ;\n Q\n", "FOO"), "M-XINDX-017"); n != 1 {
		t.Errorf("mismatched first label should fire once, got %d", n)
	}
	if n := countRuleID(lintX(t, "FOO ;\n Q\n", "FOO"), "M-XINDX-017"); n != 0 {
		t.Errorf("matching first label should be clean, got %d", n)
	}
	// No routine name (plain Lint) ⇒ no-op.
	l := newLinter(t, lint.Profile("all"))
	f, _ := l.Lint(context.Background(), []byte("BAR ;\n Q\n"))
	if countRuleID(f, "M-XINDX-017") != 0 {
		t.Errorf("unnamed lint should not fire M-XINDX-017, got %+v", f)
	}
}

// M-XINDX-021 — tree-sitter ERROR surfaces as a general syntax error.
func TestXindex021SyntaxError(t *testing.T) {
	if countRuleID(lintX(t, "FOO ;\n S X=\n W )(\n", "FOO"), "M-XINDX-021") == 0 {
		t.Error("expected M-XINDX-021 on malformed source")
	}
	if countRuleID(lintX(t, "FOO ;\n S X=1\n Q\n", "FOO"), "M-XINDX-021") != 0 {
		t.Error("clean source should not raise M-XINDX-021")
	}
}

// Clean cases: rules that must NOT fire on well-formed counter-examples.
func TestXindexCleanCounterExamples(t *testing.T) {
	cases := []struct {
		name, id, src string
	}{
		{"HANG not HALT", "M-XINDX-032", "FOO ;\n H 5\n"},
		{"READ with timeout", "M-XINDX-033", "FOO ;\n R X:10\n"},
		{"LOCK with timeout", "M-XINDX-060", "FOO ;\n L ^X:5\n"},
		{"incremental LOCK", "M-XINDX-061", "FOO ;\n L +^X:5\n"},
		{"standard Z command-free", "M-XINDX-002", "FOO ;\n W 1\n"},
		{"subscripted global kill", "M-XINDX-024", "FOO ;\n K ^GBL(1)\n"},
		{"argumented kill", "M-XINDX-023", "FOO ;\n K X\n"},
		{"QUIT with postcond not dead", "M-XINDX-009", "FOO ;\n Q:X  W 1\n"},
		{"uppercase command", "M-XINDX-047", "FOO ;\n W 1\n"},
		{"uppercase local", "M-XINDX-057", "FOO ;\n S ABC=1\n"},
		{"standard $Z ISV", "M-XINDX-028", "FOO ;\n W $ZB\n"},
		{"IF with body", "M-XINDX-051", "FOO ;\n I X>0  W 1\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if n := countRuleID(lintX(t, tc.src, "FOO"), tc.id); n != 0 {
				t.Errorf("%s should be clean for %s on %q, got %d", tc.name, tc.id, tc.src, n)
			}
		})
	}
}

// Profile/tag membership: xindex ⊆ all; sac ⊆ xindex; vista ⊆ xindex; the
// modernization profiles never pull xindex rules.
func TestXindexProfileMembership(t *testing.T) {
	xindex := ruleIDSet(lint.Profile("xindex"))
	sac := ruleIDSet(lint.Profile("sac"))
	vista := ruleIDSet(lint.Profile("vista"))
	all := ruleIDSet(lint.Profile("all"))
	def := ruleIDSet(lint.Profile("default"))

	if len(xindex) < 39 {
		t.Errorf("xindex profile has %d rules, want >= 39", len(xindex))
	}
	for id := range xindex {
		if !all[id] {
			t.Errorf("all profile missing xindex rule %s", id)
		}
	}
	for id := range sac {
		if !xindex[id] {
			t.Errorf("sac rule %s not in xindex", id)
		}
	}
	for id := range vista {
		if !xindex[id] {
			t.Errorf("vista rule %s not in xindex", id)
		}
	}
	// The default (modernization) profile pulls no XINDEX rules.
	for id := range def {
		if xindex[id] {
			t.Errorf("default profile unexpectedly contains xindex rule %s", id)
		}
	}
	// A few anchors.
	for _, want := range []string{"M-XINDX-002", "M-XINDX-019", "M-XINDX-054"} {
		if !xindex[want] {
			t.Errorf("xindex profile missing %s", want)
		}
	}
	if !sac["M-XINDX-019"] || sac["M-XINDX-013"] {
		t.Error("sac membership wrong: 019 should be sac, 013 should not")
	}
	if !vista["M-XINDX-054"] || vista["M-XINDX-019"] {
		t.Error("vista membership wrong: 054 should be vista, 019 should not")
	}
}

// M-XINDX-062 / 044 reproduce XINDEX's real SAC-line M-patterns (validated 1:1
// against the VistA corpus ground truth): first line needs a SITE/DEV-
// uppercase author prefix; 2nd line needs ;;version;package;…; both skip
// routines of <=2 lines (the XINDEX LC>2 guard).
func TestXindexSACLines(t *testing.T) {
	// Clean: real-world compliant header (A2APATCK shape) — neither fires.
	clean := "A2APATCK ;WASH/PEH - CHECK PATCHED ROUTINES ;2/13/01\n ;;1.0;TEST;;20260529\n Q\n"
	f := lintX(t, clean, "A2APATCK")
	if countRuleID(f, "M-XINDX-062") != 0 || countRuleID(f, "M-XINDX-044") != 0 {
		t.Errorf("compliant header should pass 062/044, got %+v", f)
	}

	// First line with no SITE/DEV- author prefix (digits/spaces, no `/...-`) → 062.
	bad62 := "A2APCOPY ;JA/WASH COPY PATCHES(11005) TO 1200035 ;11/17/98\n ;;1.0;TEST;;1\n Q\n"
	if countRuleID(lintX(t, bad62, "A2APCOPY"), "M-XINDX-062") != 1 {
		t.Errorf("non-SAC first line should fire 062 once on %q", bad62)
	}

	// 2nd line without the ;;version;package; structure → 044 (and 062 stays clean).
	bad44 := "A2APATCK ;WASH/PEH - CHECK ROUTINES ;2/13/01\n ;;1.0\n Q\n"
	g := lintX(t, bad44, "A2APATCK")
	if countRuleID(g, "M-XINDX-044") != 1 {
		t.Errorf("non-SAC 2nd line should fire 044 once, got %+v", g)
	}
	if countRuleID(g, "M-XINDX-062") != 0 {
		t.Errorf("compliant first line should not fire 062, got %+v", g)
	}

	// <=2 lines: the LC>2 guard suppresses both even with non-compliant lines.
	short := "BAR ;junk no slash\n ;;bad\n"
	h := lintX(t, short, "BAR")
	if countRuleID(h, "M-XINDX-062") != 0 || countRuleID(h, "M-XINDX-044") != 0 {
		t.Errorf("<=2-line routine should be exempt (LC>2 guard), got %+v", h)
	}
}

func ruleIDSet(rules []lint.Rule) map[string]bool {
	m := map[string]bool{}
	for _, r := range rules {
		m[r.ID] = true
	}
	return m
}

func repeat(s string, n int) string {
	b := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		b = append(b, s...)
	}
	return string(b)
}
