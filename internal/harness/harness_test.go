package harness_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/harness"
	"github.com/vista-cloud-dev/m-cli/internal/mcov"
	"github.com/vista-cloud-dev/m-cli/internal/mtest"
)

func goldenFrame(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("testdata/frame_basic.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSplitFrameMeta(t *testing.T) {
	suites, lcov, meta, err := harness.SplitFrame(goldenFrame(t))
	if err != nil {
		t.Fatalf("SplitFrame: %v", err)
	}
	if meta.Frame != 1 || meta.Tier != "integration" || meta.Engine != "iris" || meta.NS != "VEHU" {
		t.Errorf("meta header = %+v", meta)
	}
	if meta.Suites != 2 || meta.Pass != 2 || meta.Fail != 1 {
		t.Errorf("meta trailer = suites %d pass %d fail %d, want 2/2/1", meta.Suites, meta.Pass, meta.Fail)
	}
	if len(suites) != 2 {
		t.Fatalf("got %d suite blocks, want 2", len(suites))
	}
	if lcov == "" || !strings.Contains(lcov, "SF:MATH.m") {
		t.Errorf("lcov block missing SF:MATH.m: %q", lcov)
	}
}

// The whole point of the frame-as-contract: each per-suite block is verbatim
// ^STDASSERT text that the UNCHANGED mtest.ParseOutput consumes — no new parse
// logic in the splitter.
func TestSplitFrameSuiteBlocksRoundTripThroughMtest(t *testing.T) {
	suites, _, _, err := harness.SplitFrame(goldenFrame(t))
	if err != nil {
		t.Fatalf("SplitFrame: %v", err)
	}
	want := map[string]struct {
		total, passed, failed int
		ok                    bool
	}{
		"MATHTST":    {2, 1, 1, false},
		"FILEMANTST": {1, 1, 0, true},
	}
	for _, sb := range suites {
		w, ok := want[sb.Name]
		if !ok {
			t.Errorf("unexpected suite %q", sb.Name)
			continue
		}
		if sb.Exit != 0 {
			t.Errorf("%s exit = %d, want 0", sb.Name, sb.Exit)
		}
		s := mtest.ParseOutput(sb.Body)
		if s.Total != w.total || s.Passed != w.passed || s.Failed != w.failed || s.OK != w.ok {
			t.Errorf("%s ParseOutput = %d/%d/%d ok=%v, want %d/%d/%d ok=%v",
				sb.Name, s.Total, s.Passed, s.Failed, s.OK, w.total, w.passed, w.failed, w.ok)
		}
	}
}

// The ##LCOV block is verbatim LCOV the UNCHANGED mcov consumers understand.
func TestSplitFrameLCOVRoundTripsThroughMcov(t *testing.T) {
	_, lcov, _, err := harness.SplitFrame(goldenFrame(t))
	if err != nil {
		t.Fatalf("SplitFrame: %v", err)
	}
	r, err := mcov.ParseLCOV(lcov)
	if err != nil {
		t.Fatalf("ParseLCOV: %v", err)
	}
	bf := mcov.ByFile(r)
	if len(bf) != 1 || bf[0].Path != "MATH.m" || bf[0].Total != 2 || bf[0].Covered != 1 {
		t.Errorf("ByFile = %+v, want MATH.m 1/2", bf)
	}
}

func TestSplitFrameTruncatedStreamDetected(t *testing.T) {
	full := goldenFrame(t)
	// Drop the ##END-HARNESS trailer — a dropped connection.
	cut := full[:strings.Index(full, "##END-HARNESS")]
	suites, _, _, err := harness.SplitFrame(cut)
	if !errors.Is(err, harness.ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
	// Partial results still come back so a caller can render what arrived.
	if len(suites) != 2 {
		t.Errorf("got %d suites, want 2 (partial)", len(suites))
	}
}

func TestSplitFrameMissingHeader(t *testing.T) {
	_, _, _, err := harness.SplitFrame("##SUITE ^X\nAll tests passed.\n##END ^X exit=0\n")
	if !errors.Is(err, harness.ErrNoFrame) {
		t.Fatalf("err = %v, want ErrNoFrame", err)
	}
}

// Unrecognized ## lines degrade gracefully (skipped, not fatal) — forward-compat
// with frame versions that add directives.
func TestSplitFrameUnknownDirectiveSkipped(t *testing.T) {
	frame := "##M-HARNESS frame=2 tier=pure-logic engine=ydb ns=USER\n" +
		"##FUTURE something\n" +
		"##SUITE ^XTST\nAll tests passed.\nResults: 1 tests  1 passed  0 failed\n##END ^XTST exit=0\n" +
		"##END-HARNESS suites=1 pass=1 fail=0\n"
	suites, _, meta, err := harness.SplitFrame(frame)
	if err != nil {
		t.Fatalf("SplitFrame: %v", err)
	}
	if meta.Frame != 2 || meta.Tier != "pure-logic" {
		t.Errorf("meta = %+v", meta)
	}
	if len(suites) != 1 || suites[0].Name != "XTST" {
		t.Fatalf("suites = %+v, want one XTST", suites)
	}
}
