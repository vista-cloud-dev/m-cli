package watch

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiff(t *testing.T) {
	prev := map[string]sig{"a": {1, 10}, "b": {2, 20}}
	cur := map[string]sig{"a": {1, 10}, "b": {9, 20}, "c": {3, 30}} // b modified, c added, none removed
	ev := diff(prev, cur)
	if len(ev.Changed) != 2 || ev.Changed[0] != "b" || ev.Changed[1] != "c" {
		t.Errorf("Changed = %v, want [b c]", ev.Changed)
	}
	if len(ev.Removed) != 0 {
		t.Errorf("Removed = %v, want []", ev.Removed)
	}

	ev = diff(map[string]sig{"x": {1, 1}}, map[string]sig{})
	if len(ev.Removed) != 1 || ev.Removed[0] != "x" {
		t.Errorf("Removed = %v, want [x]", ev.Removed)
	}
}

func TestWatchDetectsChange(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.m")
	if err := os.WriteFile(f, []byte("EN ;\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w := &Watcher{
		List:     func() ([]string, error) { return []string{f}, nil },
		Interval: 15 * time.Millisecond,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan Event, 8)
	go func() { _ = w.Watch(ctx, false, func(ev Event) { events <- ev }) }()

	// Let the baseline scan settle, then change the file (different size so the
	// change is detected regardless of mtime resolution).
	time.Sleep(60 * time.Millisecond)
	if err := os.WriteFile(f, []byte("EN ;\n quit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case ev := <-events:
		if len(ev.Changed) != 1 || ev.Changed[0] != f {
			t.Errorf("Changed = %v, want [%s]", ev.Changed, f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no change event within 2s")
	}
}

func TestWatchEmitsBaseline(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.m")
	if err := os.WriteFile(f, []byte("EN ;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := &Watcher{List: func() ([]string, error) { return []string{f}, nil }, Interval: time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan Event, 1)
	go func() { _ = w.Watch(ctx, true, func(ev Event) { got <- ev }) }()

	select {
	case ev := <-got:
		if len(ev.Changed) != 1 || ev.Changed[0] != f {
			t.Errorf("baseline Changed = %v, want [%s]", ev.Changed, f)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no baseline event")
	}
}
