package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/vista-cloud-dev/clikit"
	"github.com/vista-cloud-dev/m-cli/internal/config"
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

func TestDispatchedCommandsCapturePassthrough(t *testing.T) {
	// The dispatched verbs must capture every following token verbatim (Kong
	// passthrough) so sibling flags are forwarded untouched, not parsed by `m`.
	cli := &CLI{}
	k, err := kong.New(cli, kong.Name("m"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := k.Parse([]string{"pull", "--force", "R1", "--unknown-to-m"}); err != nil {
		t.Fatalf("parse passthrough: %v", err)
	}
	if got, want := cli.Pull.Rest, []string{"--force", "R1", "--unknown-to-m"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pull passthrough = %v, want %v", got, want)
	}

	cli = &CLI{}
	k, _ = kong.New(cli, kong.Name("m"))
	if _, err := k.Parse([]string{"kids", "decompose", "x.KID"}); err != nil {
		t.Fatalf("parse kids: %v", err)
	}
	if got, want := cli.Kids.Rest, []string{"decompose", "x.KID"}; !reflect.DeepEqual(got, want) {
		t.Errorf("kids passthrough = %v, want %v", got, want)
	}
}

func TestDispatchGlobals(t *testing.T) {
	cases := []struct {
		name string
		cc   *clikit.Context
		want []string
	}{
		{"json", &clikit.Context{Format: clikit.FormatJSON}, []string{"--output", "json"}},
		{"text", &clikit.Context{Format: clikit.FormatText, Color: true}, []string{"--output", "text"}},
		{"text no-color", &clikit.Context{Format: clikit.FormatText, Color: false}, []string{"--output", "text", "--no-color"}},
		{"verbose json", &clikit.Context{Format: clikit.FormatJSON, Verbose: true}, []string{"--output", "json", "--verbose"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := dispatchGlobals(tc.cc); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("dispatchGlobals = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestResolveLintFilter(t *testing.T) {
	cases := []struct {
		name    string
		flag    string
		cfgRule string
		want    string
	}{
		{"flag wins over config", "modern", "all", "modern"},
		{"config when flag is default", "default", "all", "all"},
		{"config comma-list", "default", "M-MOD-001,M-STY-001", "M-MOD-001,M-STY-001"},
		{"default when neither set", "default", "", "default"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveLintFilter(tc.flag, config.Config{LintRules: tc.cfgRule})
			if got != tc.want {
				t.Errorf("resolveLintFilter(%q, rules=%q) = %q, want %q", tc.flag, tc.cfgRule, got, tc.want)
			}
		})
	}
}
