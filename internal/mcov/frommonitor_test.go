package mcov

import "testing"

// FromMonitor joins a raw ##MON block (MLINE:routine:line:count) to the parse-
// tree executable lines — the host-side half of resident IRIS coverage. It must
// produce the same ByFile rollup the host-orchestrated runIris would from the
// same monitor data (the G4-by-construction guarantee).
func TestFromMonitor(t *testing.T) {
	// MATH.m executable lines: 3 (add) and 5 (sub). Monitor: 3 ran, 5 did not.
	mon := "MLINE:MATH:3:5\nMLINE:MATH:5:0\n"
	r, err := FromMonitor(mustParser(t), mon, []string{"testdata/MATH.m"})
	if err != nil {
		t.Fatalf("FromMonitor: %v", err)
	}
	if r.Total() != 2 || r.Covered() != 1 {
		t.Fatalf("coverage = %d/%d, want 1/2", r.Covered(), r.Total())
	}
	bf := ByFile(r)
	if len(bf) != 1 || bf[0].Path != "testdata/MATH.m" || bf[0].Covered != 1 || bf[0].Total != 2 {
		t.Errorf("ByFile = %+v, want MATH.m 1/2", bf)
	}
	// The executable-line denominator comes from the parse tree, not the monitor:
	// a comment/label line the monitor never reports is still absent from the set.
	for _, l := range r.Lines {
		if l.Line != 3 && l.Line != 5 {
			t.Errorf("unexpected covered line %d (only exec lines 3,5 belong)", l.Line)
		}
	}
}
