package flow

// DefinitelyDefined is a forward MUST-analysis (definite assignment) over cfg:
// in[B] is the set of local names guaranteed to have been DEF'd on EVERY path
// from the label entry to B. It drives M-MOD-024 (read of a local before it is
// definitely assigned).
//
// Differs from classical reaching-definitions in two ways: the lattice element
// is a set of variable *names* (we only need "is X definitely defined", not
// which SET did it), and the meet is intersection (defined on every path).
//
// formals are definitely defined at entry. Transfer for a block whose command
// ran is (in - kills) ∪ defs, or ∅ on kills_all; skip / if-skip edges (command
// did not run) carry the in-set through unchanged. Unreachable blocks → ∅.
func DefinitelyDefined(cfg CFG, src []byte, formals []string) map[int]map[string]bool {
	effs := make(map[int]*Effects, len(cfg.Blocks))
	for _, b := range cfg.Blocks {
		if b.Kind == "command" && b.HasCmd {
			e := effects(b.Command, src)
			effs[b.ID] = &e
		}
	}

	preds := make(map[int][]predEdge, len(cfg.Blocks))
	for _, b := range cfg.Blocks {
		for k, s := range b.Succ {
			preds[s] = append(preds[s], predEdge{id: b.ID, kind: b.Edges[k]})
		}
	}

	in := make(map[int]map[string]bool, len(cfg.Blocks))
	computed := map[int]bool{0: true}
	in[0] = map[string]bool{}
	for _, f := range formals {
		in[0][f] = true
	}

	work := append([]int(nil), cfg.Blocks[0].Succ...)
	queued := make(map[int]bool, len(cfg.Blocks))
	for _, s := range work {
		queued[s] = true
	}

	for len(work) > 0 {
		bid := work[0]
		work = work[1:]
		queued[bid] = false
		if bid == 0 {
			continue
		}

		var newIn map[string]bool
		seen := false
		for _, p := range preds[bid] {
			if !computed[p.id] {
				continue
			}
			out := reachOutForEdge(in[p.id], effs[p.id], p.kind)
			if !seen {
				newIn = copySet(out)
				seen = true
			} else {
				intersectInto(newIn, out)
			}
		}
		if !seen {
			continue // no predecessor computed yet — defer
		}
		if computed[bid] && setEqual(newIn, in[bid]) {
			continue
		}
		in[bid] = newIn
		computed[bid] = true
		for _, s := range cfg.Blocks[bid].Succ {
			if !queued[s] {
				work = append(work, s)
				queued[s] = true
			}
		}
	}

	for _, b := range cfg.Blocks {
		if !computed[b.ID] {
			in[b.ID] = map[string]bool{} // unreachable
		}
	}
	return in
}

// reachOutForEdge is the definite-assignment set leaving a block along an edge.
// A nil eff (entry/exit) or a skip / if-skip edge carries the in-set unchanged;
// otherwise the command ran: ∅ on kills_all, else (in - kills) ∪ defs.
func reachOutForEdge(in map[string]bool, eff *Effects, edge string) map[string]bool {
	if eff == nil || edge == "skip" || edge == "if-skip" {
		return in
	}
	if eff.KillsAll {
		return map[string]bool{}
	}
	out := make(map[string]bool, len(in)+len(eff.Defs))
	for k := range in {
		if !eff.Kills[k] {
			out[k] = true
		}
	}
	for k := range eff.Defs {
		out[k] = true
	}
	return out
}

// intersectInto removes from dst every key not present in other (set meet).
func intersectInto(dst, other map[string]bool) {
	for k := range dst {
		if !other[k] {
			delete(dst, k)
		}
	}
}
