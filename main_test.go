package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsMFile(t *testing.T) {
	cases := map[string]bool{
		"a.m": true, "B.MAC": true, "x.int": true, "DGREG.INT": true,
		"readme.md": false, "a.go": false, "noext": false,
	}
	for name, want := range cases {
		if got := isMFile(name); got != want {
			t.Errorf("isMFile(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestDiscover(t *testing.T) {
	dir := t.TempDir()
	write := func(rel string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("EN ;\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.m")
	write("b.mac")
	write("sub/c.int") // VistA via ^%RI: .int IS the source
	write("sub/skip.txt")
	write(".git/d.m") // must be skipped

	files, err := discover([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("discover dir: got %d %v, want 3 (.m/.mac/.int; skip .txt and .git)", len(files), files)
	}

	// An explicit file arg is kept as-is, even with a non-M extension.
	odd := filepath.Join(dir, "sub/skip.txt")
	files, err = discover([]string{odd})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0] != odd {
		t.Errorf("discover explicit file: got %v, want [%s]", files, odd)
	}
}
