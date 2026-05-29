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
