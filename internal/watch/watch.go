// Package watch is the host-side half of `m watch` (spec §3.1, §9): a save
// triggered inner loop that re-runs the engine-neutral checks (lint, fmt) on
// changed files. The run half (compile/test/coverage) is a later, engine-bound
// stage (4.2) — this package never executes M.
//
// It polls file signatures rather than using inotify/fsnotify, keeping the
// dependency footprint at zero (minimal-SBOM posture) and staying trivially
// cross-platform. Polling a dev working set is cheap; fsnotify is a possible
// future optimization for very large trees.
package watch

import (
	"context"
	"os"
	"sort"
	"time"
)

// Lister returns the current set of files to watch (re-evaluated each scan, so
// newly created files are picked up).
type Lister func() ([]string, error)

// Event reports the files that changed since the previous scan.
type Event struct {
	Changed []string // created or modified
	Removed []string
}

// Watcher polls the files from List every Interval and reports changes.
type Watcher struct {
	List     Lister
	Interval time.Duration
}

type sig struct {
	mod  int64
	size int64
}

func (w *Watcher) signatures() map[string]sig {
	files, err := w.List()
	if err != nil {
		return nil
	}
	out := make(map[string]sig, len(files))
	for _, f := range files {
		st, err := os.Stat(f)
		if err != nil {
			continue // vanished between list and stat
		}
		out[f] = sig{mod: st.ModTime().UnixNano(), size: st.Size()}
	}
	return out
}

// diff computes the change event between two signature snapshots.
func diff(prev, cur map[string]sig) Event {
	var ev Event
	for f, s := range cur {
		if o, ok := prev[f]; !ok || o != s {
			ev.Changed = append(ev.Changed, f)
		}
	}
	for f := range prev {
		if _, ok := cur[f]; !ok {
			ev.Removed = append(ev.Removed, f)
		}
	}
	sort.Strings(ev.Changed)
	sort.Strings(ev.Removed)
	return ev
}

// Watch scans a baseline, then polls until ctx is done, calling onChange for
// each non-empty event. If emitBaseline is set, the initial file set is
// delivered as one Changed event first (so startup checks everything). It
// returns ctx.Err() when ctx is canceled.
func (w *Watcher) Watch(ctx context.Context, emitBaseline bool, onChange func(Event)) error {
	interval := w.Interval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	prev := w.signatures()
	if emitBaseline && len(prev) > 0 {
		all := make([]string, 0, len(prev))
		for f := range prev {
			all = append(all, f)
		}
		sort.Strings(all)
		onChange(Event{Changed: all})
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			cur := w.signatures()
			ev := diff(prev, cur)
			prev = cur
			if len(ev.Changed) > 0 || len(ev.Removed) > 0 {
				onChange(ev)
			}
		}
	}
}
