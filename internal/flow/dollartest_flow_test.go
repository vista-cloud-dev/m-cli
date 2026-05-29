package flow_test

import (
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/flow"
)

func staleTestReads(t *testing.T, src string) map[string]int {
	t.Helper()
	root, b, done := parseRoot(t, src)
	defer done()
	out := map[string]int{}
	for _, cfg := range flow.BuildCFGs(root, b) {
		out[cfg.LabelName] = len(flow.StaleTestReads(cfg, b))
	}
	return out
}

// A $TEST read with no preceding $T-setter on any path is stale.
func TestStaleTestRead(t *testing.T) {
	if got := staleTestReads(t, "EN ;\n write $test\n quit\n")["EN"]; got != 1 {
		t.Errorf("stale reads = %d, want 1", got)
	}
}

// The $T abbreviation is recognized.
func TestStaleTestAbbrev(t *testing.T) {
	if got := staleTestReads(t, "EN ;\n write $t\n quit\n")["EN"]; got != 1 {
		t.Errorf("stale reads = %d, want 1 ($t)", got)
	}
}

// An IF runs on BOTH its fall and if-skip edges (it evaluates its condition,
// setting $TEST), so a later read is fresh on every path.
func TestStaleTestFreshAfterIf(t *testing.T) {
	if got := staleTestReads(t, "EN ;\n if 1 set X=2\n write $test\n quit\n")["EN"]; got != 0 {
		t.Errorf("stale reads = %d, want 0 (IF sets $TEST on both edges)", got)
	}
}

// READ is a $T-setter; a $TEST read after it is fresh.
func TestStaleTestFreshAfterRead(t *testing.T) {
	if got := staleTestReads(t, "EN ;\n read X:5\n write $test\n quit\n")["EN"]; got != 0 {
		t.Errorf("stale reads = %d, want 0 (READ sets $TEST)", got)
	}
}

// MUST-analysis: a setter behind a postconditional runs only on the true path;
// the skip path reaches the read with $TEST stale, so it is flagged.
func TestStaleTestSetterOnlyOnOnePath(t *testing.T) {
	if got := staleTestReads(t, "EN ;\n read:X Y\n write $test\n quit\n")["EN"]; got != 1 {
		t.Errorf("stale reads = %d, want 1 (READ only on the postcond-true path)", got)
	}
}
