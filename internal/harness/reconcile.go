package harness

import "github.com/vista-cloud-dev/m-cli/internal/mtest"

// Source names which tier produced a result.
const (
	SourceFileSide = "file-side"
	SourceResident = "resident"
)

// Provenanced is one suite's result tagged with the tier it is authoritative for
// and which tier actually produced it.
type Provenanced struct {
	Result mtest.RunResult
	Tier   string // mtest.TierPureLogic | mtest.TierIntegration
	Source string // SourceFileSide | SourceResident
}

// Merged is the reconciled two-tier verdict (design §3.4, spec §9.1-Q6).
type Merged struct {
	Results []Provenanced // one per suite, file-side first then resident-only, in order
	OK      bool          // union: false if ANY suite failed on either tier
}

// Reconcile produces one verdict by provenance. The file-side tier is
// authoritative for pure-logic suites (the deterministic PR gate); the resident
// IRIS tier is authoritative for integration suites (the live DD + data). The
// host runs each suite on exactly one tier, so the two result sets are normally
// disjoint; on conflict for the same suite the integration (resident) verdict
// wins — reality beats the file-side approximation. The OK is the UNION: any
// failure on either tier ⇒ not OK, so `m test`'s exit is non-zero (spec §3.3).
func Reconcile(fileSide, resident []mtest.RunResult) Merged {
	var out []Provenanced
	idx := map[string]int{}
	for _, r := range fileSide {
		idx[r.Suite] = len(out)
		out = append(out, Provenanced{Result: r, Tier: mtest.TierPureLogic, Source: SourceFileSide})
	}
	for _, r := range resident {
		p := Provenanced{Result: r, Tier: mtest.TierIntegration, Source: SourceResident}
		if i, ok := idx[r.Suite]; ok {
			out[i] = p // resident (integration) wins the conflict
			continue
		}
		idx[r.Suite] = len(out)
		out = append(out, p)
	}
	merged := Merged{Results: out, OK: true}
	for _, p := range out {
		if !p.Result.OK {
			merged.OK = false
			break
		}
	}
	return merged
}
