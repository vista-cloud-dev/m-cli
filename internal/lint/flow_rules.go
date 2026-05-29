package lint

import (
	"fmt"

	"github.com/vista-cloud-dev/m-cli/internal/flow"
	"github.com/vista-cloud-dev/m-parse/parse"
)

// M-MOD-025 — LOCK acquired but not released before the label exits. Built on
// the flow infra: a per-label CFG + a forward union dataflow of held LOCK names
// (incremental +/-, plain replace-all, argumentless release). A name still held
// at the exit block on any path is a leak — the lock outlives the routine that
// took it, so a later caller (or the same process re-entering) blocks. One
// finding per leaked name, anchored at the label header.
var ruleLockLeak = Rule{
	ID:       "M-MOD-025",
	Severity: Error,
	Category: "concurrency",
	Title:    "LOCK acquired but not released before label exit",
	Tags:     []string{"modern"},
	Inspect: func(root parse.Node, src []byte) []Finding {
		var out []Finding
		for _, cfg := range flow.BuildCFGs(root, src) {
			for _, name := range flow.HeldAtExit(cfg, src) {
				out = append(out, Finding{
					Message: fmt.Sprintf("LOCK on %s acquired in %s is never released before the label exits "+
						"(release it with an argumentless LOCK or `LOCK -%s`)", name, cfg.LabelName, name),
					Line:    cfg.LabelRow + 1,
					Col:     cfg.LabelCol + 1,
					EndLine: cfg.LabelRow + 1,
					EndCol:  cfg.LabelCol + 1 + len(cfg.LabelName),
				})
			}
		}
		return out
	},
}

// M-MOD-026 — at least one path from label entry to exit leaves a transaction
// open (path-sensitive graduation of an intra-label TSTART/TCOMMIT balance
// check). A forward MAY-analysis over the same CFG tracks the worst-case
// transaction nesting depth; a non-zero depth at the exit block means some path
// forgets to TCOMMIT/TROLLBACK, so the transaction outlives the routine.
var ruleTransactionLeak = Rule{
	ID:       "M-MOD-026",
	Severity: Error,
	Category: "concurrency",
	Title:    "TSTART leak across exit paths",
	Tags:     []string{"modern"},
	Inspect: func(root parse.Node, src []byte) []Finding {
		var out []Finding
		for _, cfg := range flow.BuildCFGs(root, src) {
			if depth := flow.DepthAtExit(cfg, src); depth > 0 {
				out = append(out, Finding{
					Message: fmt.Sprintf("transaction may be open when %s exits (max depth %d) — "+
						"TCOMMIT/TROLLBACK on every path", cfg.LabelName, depth),
					Line:    cfg.LabelRow + 1,
					Col:     cfg.LabelCol + 1,
					EndLine: cfg.LabelRow + 1,
					EndCol:  cfg.LabelCol + 1 + len(cfg.LabelName),
				})
			}
		}
		return out
	},
}

// M-MOD-017 — read of $TEST without a $T-setting command (IF/OPEN/LOCK/READ/JOB)
// guaranteed to have run on every path from the label entry. A forward
// MUST-analysis (AND meet) over the same CFG decides freshness at each block;
// reading $TEST where it is not fresh returns a value left over from before the
// label was entered — almost always a bug. One finding per (label, source line).
var ruleStaleTest = Rule{
	ID:       "M-MOD-017",
	Severity: Warning,
	Category: "bug",
	Title:    "$TEST read without preceding $T-setter (stale read)",
	Tags:     []string{"modern"},
	Inspect: func(root parse.Node, src []byte) []Finding {
		var out []Finding
		for _, cfg := range flow.BuildCFGs(root, src) {
			for _, r := range flow.StaleTestReads(cfg, src) {
				out = append(out, Finding{
					Message: fmt.Sprintf("$TEST read in %s without a $T-setting command on every "+
						"prior path — the value may be stale from a much earlier command", r.Label),
					Line:    r.Line,
					Col:     r.Col,
					EndLine: r.Line,
					EndCol:  r.EndCol,
				})
			}
		}
		return out
	},
}

// M-MOD-024 — read of a local variable that may not have been SET on every path
// from the label entry. A forward MUST-analysis (definite assignment) over the
// per-label CFG; a use of a name not in the definitely-defined set entering its
// block (and not defined by an earlier argument of the same command) is flagged.
// Formals are defined at entry; by-reference DO/JOB params are defs. One finding
// per (label, variable). The VistA Kernel auto-defined locals are suppressed
// unconditionally (see vista_kernel.go), and the IF $G(X)="" SET X idiom is
// honored. FP-prone by nature, so it carries the pedantic tag — excluded from
// the curated `default` profile but present in modern/pythonic/pedantic/all.
var ruleReadOfUndefined = Rule{
	ID:       "M-MOD-024",
	Severity: Error,
	Category: "bug",
	Title:    "Read of local variable before definite assignment",
	Tags:     []string{"modern", "pedantic"},
	Inspect: func(root parse.Node, src []byte) []Finding {
		formalsByRow := flow.FormalParams(root, src)
		var out []Finding
		for _, cfg := range flow.BuildCFGs(root, src) {
			reported := map[string]bool{}
			for _, r := range flow.UndefinedReads(cfg, src, formalsByRow[cfg.LabelRow]) {
				if reported[r.Name] || kernelAutoDefined[r.Name] {
					continue
				}
				reported[r.Name] = true
				out = append(out, Finding{
					Message: fmt.Sprintf("local %q may be read before being definitely defined "+
						"on every path from %s", r.Name, r.Label),
					Line:    r.Line,
					Col:     r.Col,
					EndLine: r.Line,
					EndCol:  r.EndCol,
				})
			}
		}
		return out
	},
}

// M-MOD-036 — untrusted data flows into an indirection / XECUTE sink: the
// differentiating security rule. A forward MAY-analysis (taint, union meet) over
// the same per-label CFG tracks which locals may hold attacker-controlled data
// (sources: READ X, and public-label formals); a finding fires when such a
// tainted local reaches an @indirection node (D @X, S @X=v, S Y=@X, S Y=A_@X) or
// an XECUTE argument — M's code/SQL/path-injection vector. $L/$LENGTH/$A/$ASCII
// sanitize (numeric output). One finding per (label, tainted var), anchored at
// the sink. Tagged `modern` (in `default`): unlike M-MOD-024, it only fires at
// the rare indirection/XECUTE sink, so its signal-to-noise warrants being on by
// default — and as the suite's headline security check it must be discoverable.
var ruleTaintToSink = Rule{
	ID:       "M-MOD-036",
	Severity: Error,
	Category: "security",
	Title:    "Untrusted data flows into an indirection sink",
	Tags:     []string{"modern"},
	Inspect: func(root parse.Node, src []byte) []Finding {
		formalsByRow := flow.FormalParams(root, src)
		config := flow.DefaultTaintConfig()
		var out []Finding
		for _, cfg := range flow.BuildCFGs(root, src) {
			reported := map[string]bool{}
			for _, fl := range flow.TaintFlows(cfg, src, formalsByRow[cfg.LabelRow], config) {
				if reported[fl.Name] {
					continue
				}
				reported[fl.Name] = true
				out = append(out, Finding{
					Message: fmt.Sprintf("tainted local %q flows into %s in %s — possible "+
						"code/SQL/path injection", fl.Name, fl.SinkKind, fl.Label),
					Line:    fl.Line,
					Col:     fl.Col,
					EndLine: fl.EndLine,
					EndCol:  fl.EndCol,
				})
			}
		}
		return out
	},
}

// M-MOD-027 — `SET $ETRAP=...` not preceded by `NEW $ETRAP` on every path from
// the label entry (path-sensitive graduation of an intra-label NEW-$ETRAP
// check). Setting the error trap without first NEW-ing it persists the new
// handler past the label exit into whatever the caller stacked — almost always
// a bug. A forward MUST-analysis over the same CFG decides protection at each
// SET site; an unprotected site is flagged at the offending command.
var ruleEtrapLeak = Rule{
	ID:       "M-MOD-027",
	Severity: Error,
	Category: "bug",
	Title:    "SET $ETRAP without NEW $ETRAP on every path",
	Tags:     []string{"modern"},
	Inspect: func(root parse.Node, src []byte) []Finding {
		var out []Finding
		for _, cfg := range flow.BuildCFGs(root, src) {
			for _, leak := range flow.EtrapLeaks(cfg, src) {
				out = append(out, Finding{
					Message: fmt.Sprintf("SET $ETRAP without a preceding NEW $ETRAP on every path from %s "+
						"— the handler escapes the label", leak.Label),
					Line:    leak.Line,
					Col:     leak.Col,
					EndLine: leak.Line,
					EndCol:  leak.EndCol,
				})
			}
		}
		return out
	},
}
