package lint_test

import (
	"context"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/config"
	"github.com/vista-cloud-dev/m-cli/internal/lint"
)

func hasRule(rules []lint.Rule, id string) bool {
	for _, r := range rules {
		if r.ID == id {
			return true
		}
	}
	return false
}

func ruleSeverity(rules []lint.Rule, id string) lint.Severity {
	for _, r := range rules {
		if r.ID == id {
			return r.Severity
		}
	}
	return ""
}

// A profile-name filter resolves to that profile's rules.
func TestResolveProfileName(t *testing.T) {
	rules, err := lint.Resolve("default", lint.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != len(lint.Profile("default")) {
		t.Errorf("Resolve(default) = %d rules, want %d", len(rules), len(lint.Profile("default")))
	}
	// M-MOD-024 is pedantic ⇒ out of default.
	if hasRule(rules, "M-MOD-024") {
		t.Error("default profile should not contain M-MOD-024")
	}
}

// A comma-list mixing a rule ID and a profile name unions and dedups.
func TestResolveCommaList(t *testing.T) {
	rules, err := lint.Resolve("M-MOD-024,M-STY-001", lint.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 2 || !hasRule(rules, "M-MOD-024") || !hasRule(rules, "M-STY-001") {
		t.Errorf("Resolve(comma list) = %+v, want exactly the two ids", ruleIDs(rules))
	}
}

func TestResolveUnknownProfile(t *testing.T) {
	if _, err := lint.Resolve("bogus", lint.DefaultOptions()); err == nil {
		t.Fatal("want error for unknown profile")
	}
}

func TestResolveUnknownRuleID(t *testing.T) {
	if _, err := lint.Resolve("M-MOD-999", lint.DefaultOptions()); err == nil {
		t.Fatal("want error for unknown rule id")
	}
}

// [lint] disable drops a rule from the selected set.
func TestResolveDisable(t *testing.T) {
	opts := lint.OptionsFromConfig(config.Config{LintDisable: []string{"M-MOD-001"}})
	rules, err := lint.Resolve("all", opts)
	if err != nil {
		t.Fatal(err)
	}
	if hasRule(rules, "M-MOD-001") {
		t.Error("M-MOD-001 should be dropped by disable")
	}
	if !hasRule(rules, "M-MOD-037") {
		t.Error("other rules should remain")
	}
}

// [lint.severity] re-stamps a rule's severity at build time.
func TestResolveSeverityOverride(t *testing.T) {
	opts := lint.OptionsFromConfig(config.Config{
		LintSeverityOverrides: map[string]string{"M-MOD-001": "warning"},
	})
	rules, err := lint.Resolve("all", opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := ruleSeverity(rules, "M-MOD-001"); got != lint.Warning {
		t.Errorf("M-MOD-001 severity = %q, want warning", got)
	}
}

// A lowered line_length threshold flips a finding on.
func TestOptionsThresholdLineLength(t *testing.T) {
	opts := lint.OptionsFromConfig(config.Config{LintThresholds: map[string]int{"line_length": 10}})
	l := newLinter(t, lint.ProfileWith("default", opts))
	src := []byte("EN ; this line is clearly more than ten columns wide\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	if !findingHas(findings, "M-MOD-001") {
		t.Errorf("want M-MOD-001 with line_length=10, got %+v", findings)
	}
}

// [lint.taint] formals_tainted=false silences a formal-sourced M-MOD-036.
func TestOptionsFormalsTaintedFalse(t *testing.T) {
	src := []byte("EN(ARG) ;\n xecute ARG\n quit\n")

	on := newLinter(t, lint.ProfileWith("default", lint.DefaultOptions()))
	if f, err := on.Lint(context.Background(), src); err != nil || !findingHas(f, "M-MOD-036") {
		t.Fatalf("default should taint formals → M-MOD-036, got %+v err %v", f, err)
	}

	ft := false
	opts := lint.OptionsFromConfig(config.Config{LintTaintFormalsTainted: &ft})
	off := newLinter(t, lint.ProfileWith("default", opts))
	if f, err := off.Lint(context.Background(), src); err != nil || findingHas(f, "M-MOD-036") {
		t.Fatalf("formals_tainted=false should silence M-MOD-036, got %+v err %v", f, err)
	}
}

// kernel_locals = "default" opts the M-MOD-024 allowlist in; an empty config
// keeps it strict.
func TestOptionsKernelLocalsDefault(t *testing.T) {
	src := []byte("EN ;\n write U\n quit\n")

	strict := newLinter(t, lint.ProfileWith("modern", lint.DefaultOptions()))
	if f, err := strict.Lint(context.Background(), src); err != nil || !findingHas(f, "M-MOD-024") {
		t.Fatalf("strict default should flag U, got %+v err %v", f, err)
	}

	opts := lint.OptionsFromConfig(config.Config{LintVistaKernelLocals: []string{"default"}})
	allow := newLinter(t, lint.ProfileWith("modern", opts))
	if f, err := allow.Lint(context.Background(), src); err != nil || findingHas(f, "M-MOD-024") {
		t.Fatalf("kernel_locals=default should suppress U, got %+v err %v", f, err)
	}
}

// An explicit kernel_locals list suppresses only the listed names.
func TestOptionsKernelLocalsExplicit(t *testing.T) {
	opts := lint.OptionsFromConfig(config.Config{LintVistaKernelLocals: []string{"U"}})
	l := newLinter(t, lint.ProfileWith("modern", opts))
	src := []byte("EN ;\n write U,DATA\n quit\n")
	findings, err := l.Lint(context.Background(), src)
	if err != nil {
		t.Fatal(err)
	}
	// U is allowlisted; DATA still fires.
	if len(findings) != 1 || findings[0].Rule != "M-MOD-024" {
		t.Fatalf("got %+v, want only DATA flagged", findings)
	}
}

func ruleIDs(rules []lint.Rule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = r.ID
	}
	return out
}

func findingHas(findings []lint.Finding, id string) bool {
	for _, f := range findings {
		if f.Rule == id {
			return true
		}
	}
	return false
}
