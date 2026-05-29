package flow_test

import (
	"sort"
	"testing"

	"github.com/vista-cloud-dev/m-cli/internal/flow"
)

// taintedAtExit runs AnalyzeTaint over the single label in src and returns the
// sorted set of names tainted at the exit block.
func taintedAtExit(t *testing.T, src string, formalsTainted bool) []string {
	t.Helper()
	root, b, done := parseRoot(t, src)
	defer done()
	cfgs := flow.BuildCFGs(root, b)
	if len(cfgs) != 1 {
		t.Fatalf("got %d CFGs, want 1", len(cfgs))
	}
	cfg := cfgs[0]
	formals := flow.FormalParams(root, b)[cfg.LabelRow]
	cfgcfg := flow.DefaultTaintConfig()
	cfgcfg.FormalsTainted = formalsTainted
	sets := flow.AnalyzeTaint(cfg, b, formals, cfgcfg)
	got := []string{}
	for k := range sets[cfg.ExitID()] {
		got = append(got, k)
	}
	sort.Strings(got)
	return got
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestAnalyzeTaint(t *testing.T) {
	// formalsTainted defaults true (the faithful default); disableFormals flips
	// it off for the one case that needs to prove the knob.
	cases := []struct {
		name           string
		src            string
		disableFormals bool
		want           []string
	}{
		{
			name: "read source taints var",
			src:  "EN ;\n read X\n quit\n",
			want: []string{"X"},
		},
		{
			name: "set propagates taint",
			src:  "EN ;\n read X\n set A=X\n quit\n",
			want: []string{"A", "X"},
		},
		{
			name: "per-argument propagation S A=X,B=A",
			src:  "EN ;\n read X\n set A=X,B=A\n quit\n",
			want: []string{"A", "B", "X"},
		},
		{
			name: "strong untaint: clean RHS clears prior taint",
			src:  "EN ;\n read X\n set X=1\n quit\n",
			want: []string{},
		},
		{
			name: "set from clean var does not taint",
			src:  "EN ;\n set A=B\n quit\n",
			want: []string{},
		},
		{
			name: "sanitizer cleans the value",
			src:  "EN ;\n read X\n set A=$L(X)\n quit\n",
			want: []string{"X"}, // X still tainted, A clean
		},
		{
			name: "kill untaints",
			src:  "EN ;\n read X\n kill X\n quit\n",
			want: []string{},
		},
		{
			name: "argumentless kill clears all",
			src:  "EN ;\n read X\n read Y\n kill\n quit\n",
			want: []string{},
		},
		{
			name: "new untaints its target",
			src:  "EN ;\n read X\n new X\n quit\n",
			want: []string{},
		},
		{
			name: "by-reference DO call taints arg",
			src:  "EN ;\n do LBL(.X,Y)\n quit\n",
			want: []string{"X"},
		},
		{
			name: "by-reference in extrinsic on SET RHS taints arg",
			src:  "EN ;\n set R=$$F(.X)\n quit\n",
			want: []string{"X"}, // X tainted by-ref; R clean (the .X identifier is not a read)
		},
		{
			name: "formals tainted at entry",
			src:  "EN(A,B) ;\n quit\n",
			want: []string{"A", "B"},
		},
		{
			name:           "formals not tainted when disabled",
			src:            "EN(A,B) ;\n quit\n",
			disableFormals: true,
			want:           []string{},
		},
		{
			name: "merge propagates like set",
			src:  "EN ;\n read X\n merge A=X\n quit\n",
			want: []string{"A", "X"},
		},
		{
			name: "union over branches (one path taints)",
			src:  "EN ;\n if $$C() read X\n quit\n",
			want: []string{"X"}, // READ X runs only when the IF is true; union keeps X tainted at exit
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := taintedAtExit(t, tc.src, !tc.disableFormals)
			if !eqStrs(got, tc.want) {
				t.Errorf("tainted at exit = %v, want %v", got, tc.want)
			}
		})
	}
}

func taintFlows(t *testing.T, src string) []flow.TaintFlow {
	t.Helper()
	root, b, done := parseRoot(t, src)
	defer done()
	var out []flow.TaintFlow
	formalsByRow := flow.FormalParams(root, b)
	for _, cfg := range flow.BuildCFGs(root, b) {
		out = append(out, flow.TaintFlows(cfg, b, formalsByRow[cfg.LabelRow], flow.DefaultTaintConfig())...)
	}
	return out
}

func TestTaintFlows(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		wantVars []string // first-tainted-name per flow, sorted+deduped for comparison
	}{
		{
			name:     "tainted READ into D @X",
			src:      "EN ;\n read X\n do @X\n quit\n",
			wantVars: []string{"X"},
		},
		{
			name:     "tainted formal into XECUTE",
			src:      "EN(CODE) ;\n xecute CODE\n quit\n",
			wantVars: []string{"CODE"},
		},
		{
			name:     "tainted READ into S @X=v",
			src:      "EN ;\n read X\n set @X=1\n quit\n",
			wantVars: []string{"X"},
		},
		{
			name:     "tainted READ into S Y=@X",
			src:      "EN ;\n read X\n set Y=@X\n quit\n",
			wantVars: []string{"X"},
		},
		{
			name:     "tainted READ into concatenated indirection S Y=A_@X",
			src:      "EN ;\n read X\n set Y=A_@X\n quit\n",
			wantVars: []string{"X"},
		},
		{
			name:     "sanitized value into XECUTE is clean",
			src:      "EN(CODE) ;\n set S=$L(CODE)\n xecute S\n quit\n",
			wantVars: []string{}, // S is clean (sanitized); CODE not used at the sink
		},
		{
			name:     "clean indirection (untainted var) not flagged",
			src:      "EN ;\n set X=1\n do @X\n quit\n",
			wantVars: []string{},
		},
		{
			name:     "untainted XECUTE literal not flagged",
			src:      "EN ;\n xecute \"write 1\"\n quit\n",
			wantVars: []string{},
		},
		{
			name:     "strong-untainted var into indirection not flagged",
			src:      "EN ;\n read X\n set X=42\n do @X\n quit\n",
			wantVars: []string{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			flows := taintFlows(t, tc.src)
			seen := map[string]bool{}
			var got []string
			for _, f := range flows {
				if !seen[f.Name] {
					seen[f.Name] = true
					got = append(got, f.Name)
				}
			}
			sort.Strings(got)
			if got == nil {
				got = []string{}
			}
			if !eqStrs(got, tc.wantVars) {
				t.Errorf("tainted sink vars = %v, want %v (flows=%+v)", got, tc.wantVars, flows)
			}
		})
	}
}
