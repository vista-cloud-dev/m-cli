package mcov

import "testing"

// ParseLCOV reads an LCOV tracefile back into a Result so a coverage payload
// that arrived as text (e.g. the resident harness's ##LCOV frame block) joins
// the same ByFile / Percent consumers a host-side mcov.Run produces.
func TestParseLCOVRoundTrip(t *testing.T) {
	orig := Result{Lines: []LineCov{
		{Path: "MATH.m", Line: 3, Hits: 1},
		{Path: "MATH.m", Line: 4, Hits: 0},
		{Path: "STR.m", Line: 7, Hits: 3},
	}}
	got, err := ParseLCOV(LCOV(orig))
	if err != nil {
		t.Fatalf("ParseLCOV: %v", err)
	}
	// ByFile rollup must match the original — the parity contract.
	a, b := ByFile(orig), ByFile(got)
	if len(a) != len(b) {
		t.Fatalf("ByFile len %d != %d", len(a), len(b))
	}
	for i := range a {
		if a[i].Path != b[i].Path || a[i].Total != b[i].Total || a[i].Covered != b[i].Covered {
			t.Errorf("file %d: got %+v, want %+v", i, b[i], a[i])
		}
	}
}

func TestParseLCOVToleratesExtraRecords(t *testing.T) {
	// TN:/LF:/LH:/blank lines and an unrelated leading comment are ignored;
	// only SF:/DA:/end_of_record carry data.
	text := "TN:\nSF:FOO.m\nDA:1,2\nDA:2,0\nLF:2\nLH:1\nend_of_record\n"
	r, err := ParseLCOV(text)
	if err != nil {
		t.Fatalf("ParseLCOV: %v", err)
	}
	if r.Total() != 2 || r.Covered() != 1 {
		t.Errorf("got %d/%d, want 1/2", r.Covered(), r.Total())
	}
	bf := ByFile(r)
	if len(bf) != 1 || bf[0].Path != "FOO.m" {
		t.Fatalf("ByFile = %+v, want one FOO.m", bf)
	}
}

func TestParseLCOVEmpty(t *testing.T) {
	r, err := ParseLCOV("")
	if err != nil {
		t.Fatalf("ParseLCOV(empty): %v", err)
	}
	if r.Total() != 0 {
		t.Errorf("Total = %d, want 0", r.Total())
	}
}
