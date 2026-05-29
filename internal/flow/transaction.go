package flow

import "github.com/vista-cloud-dev/m-parse/parse"

var (
	tstartKW    = map[string]bool{"TS": true, "TSTART": true}
	tcommitKW   = map[string]bool{"TC": true, "TCOMMIT": true}
	trollbackKW = map[string]bool{"TRO": true, "TROLLBACK": true}
)

// maxTxDepth saturates the transaction-nesting lattice so it has a finite top,
// guaranteeing the worklist converges even on pathological input. Real M labels
// never nest more than a handful of TSTARTs.
const maxTxDepth = 32

// DepthAtExit returns the worst-case (maximum) open-transaction nesting depth on
// any path reaching the label's exit. A non-zero result means at least one path
// leaves a transaction open — the TSTART-leak signal (M-MOD-026).
func DepthAtExit(cfg CFG, src []byte) int {
	return analyzeTransactions(cfg, src)[cfg.ExitID()]
}

// analyzeTransactions runs a forward MAY-analysis over cfg with `max` as the
// meet (worst-case depth is where leaks live): TSTART increments the depth,
// TCOMMIT/TROLLBACK decrement it (floored at 0). Argumented `TROLLBACK n` is
// over-approximated as a single decrement.
func analyzeTransactions(cfg CFG, src []byte) map[int]int {
	preds := make(map[int][]predEdge, len(cfg.Blocks))
	for _, b := range cfg.Blocks {
		for k, s := range b.Succ {
			preds[s] = append(preds[s], predEdge{id: b.ID, kind: b.Edges[k]})
		}
	}

	depth := make(map[int]int, len(cfg.Blocks))
	work := make([]int, 0, len(cfg.Blocks))
	queued := make(map[int]bool, len(cfg.Blocks))
	for _, b := range cfg.Blocks {
		work = append(work, b.ID)
		queued[b.ID] = true
	}

	for len(work) > 0 {
		bid := work[0]
		work = work[1:]
		queued[bid] = false
		if bid == 0 { // entry is always depth 0
			continue
		}
		newDepth := 0
		for _, p := range preds[bid] {
			if d := txOutForEdge(depth[p.id], cfg.Blocks[p.id], p.kind, src); d > newDepth {
				newDepth = d
			}
		}
		if newDepth == depth[bid] {
			continue
		}
		depth[bid] = newDepth
		for _, s := range cfg.Blocks[bid].Succ {
			if !queued[s] {
				work = append(work, s)
				queued[s] = true
			}
		}
	}
	return depth
}

func txOutForEdge(in int, b Block, edge string, src []byte) int {
	if !b.HasCmd || edge == "skip" || edge == "if-skip" {
		return in
	}
	return applyTxCommand(in, b.Command, src)
}

func applyTxCommand(in int, cmd parse.Node, src []byte) int {
	switch kw := commandKeyword(cmd, src); {
	case tstartKW[kw]:
		if in+1 > maxTxDepth {
			return maxTxDepth
		}
		return in + 1
	case tcommitKW[kw], trollbackKW[kw]:
		if in-1 < 0 {
			return 0
		}
		return in - 1
	default:
		return in
	}
}
