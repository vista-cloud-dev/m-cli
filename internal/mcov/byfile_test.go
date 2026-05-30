package mcov

import "testing"

// ByFile rolls a Result up per source file, preserving first-seen order, with
// Covered counting lines that ran at least once.
func TestByFile(t *testing.T) {
	r := Result{Lines: []LineCov{
		{Path: "MATH.m", Hits: 1},
		{Path: "MATH.m", Hits: 0},
		{Path: "STR.m", Hits: 3},
		{Path: "MATH.m", Hits: 2}, // out-of-order line for an already-seen file
	}}
	got := ByFile(r)
	if len(got) != 2 {
		t.Fatalf("ByFile returned %d files, want 2: %+v", len(got), got)
	}
	// First-seen order: MATH.m before STR.m.
	if got[0].Path != "MATH.m" || got[0].Total != 3 || got[0].Covered != 2 {
		t.Errorf("got[0] = %+v, want MATH.m 2/3", got[0])
	}
	if got[1].Path != "STR.m" || got[1].Total != 1 || got[1].Covered != 1 {
		t.Errorf("got[1] = %+v, want STR.m 1/1", got[1])
	}
	if p := got[0].Percent(); p < 66.6 || p > 66.7 {
		t.Errorf("MATH.m percent = %.2f, want ~66.67", p)
	}
}

func TestByFileEmpty(t *testing.T) {
	if got := ByFile(Result{}); len(got) != 0 {
		t.Errorf("ByFile(empty) = %+v, want none", got)
	}
}
