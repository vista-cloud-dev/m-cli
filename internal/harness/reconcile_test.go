package harness_test

import (
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/harness"
	"github.com/vista-cloud-dev/m-cli/internal/mtest"
)

func ok(suite string) mtest.RunResult {
	return mtest.RunResult{Suite: suite, OK: true, Summary: mtest.Summary{Total: 1, Passed: 1, OK: true}}
}
func fail(suite string) mtest.RunResult {
	return mtest.RunResult{Suite: suite, OK: false, Summary: mtest.Summary{Total: 1, Failed: 1, OK: false}}
}

// Reconcile is the §9.1-Q6 rule: file-side is authoritative for pure-logic
// suites, resident for integration suites; the verdict is the UNION (any failure
// on either tier ⇒ not OK), and each suite is tagged by provenance.
func TestReconcileUnionAndProvenance(t *testing.T) {
	fileSide := []mtest.RunResult{ok("MATHTST"), ok("STRTST")}
	resident := []mtest.RunResult{fail("DGINTTST")}

	m := harness.Reconcile(fileSide, resident)
	if m.OK {
		t.Error("OK = true, want false (DGINTTST failed on the resident tier — union)")
	}
	if len(m.Results) != 3 {
		t.Fatalf("got %d results, want 3", len(m.Results))
	}
	byName := map[string]harness.Provenanced{}
	for _, r := range m.Results {
		byName[r.Result.Suite] = r
	}
	if p := byName["MATHTST"]; p.Tier != mtest.TierPureLogic || p.Source != harness.SourceFileSide {
		t.Errorf("MATHTST = %+v, want pure-logic/file-side", p)
	}
	if p := byName["DGINTTST"]; p.Tier != mtest.TierIntegration || p.Source != harness.SourceResident {
		t.Errorf("DGINTTST = %+v, want integration/resident", p)
	}
}

func TestReconcileAllPass(t *testing.T) {
	m := harness.Reconcile([]mtest.RunResult{ok("A")}, []mtest.RunResult{ok("B")})
	if !m.OK {
		t.Errorf("OK = false, want true (both tiers passed)")
	}
}

// On conflict for the SAME suite, the integration (resident) verdict wins —
// reality from the live DD beats the file-side approximation.
func TestReconcileConflictResidentWins(t *testing.T) {
	m := harness.Reconcile([]mtest.RunResult{ok("X")}, []mtest.RunResult{fail("X")})
	if len(m.Results) != 1 {
		t.Fatalf("got %d results, want 1 (deduped)", len(m.Results))
	}
	r := m.Results[0]
	if r.Source != harness.SourceResident || r.Tier != mtest.TierIntegration {
		t.Errorf("X = %+v, want resident/integration (server wins conflict)", r)
	}
	if m.OK {
		t.Error("OK = true, want false (resident verdict for X is a failure)")
	}
}
