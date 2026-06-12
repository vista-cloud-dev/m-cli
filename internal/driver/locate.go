package driver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// LocateDeps are the injectable lookups used to find a driver binary (so the
// resolution order is unit-testable without a real filesystem/PATH).
type LocateDeps struct {
	Getenv   func(string) string          // environment lookup
	LookPath func(string) (string, error) // PATH resolution
	ExeDir   func() (string, error)       // directory of the running `m` executable
	IsFile   func(string) bool            // path exists and is an executable file
}

// Locate finds the m-<engine> driver binary, in the contract's resolution order
// (driver-contract.md §4): $M_<ENGINE>_BIN → next to the `m` executable → the
// sibling source checkout's dist/ (…/m-<engine>/dist/m-<engine>) → $PATH.
func Locate(engine string, d LocateDeps) (string, error) {
	bin := "m-" + engine
	if v := d.Getenv("M_" + strings.ToUpper(engine) + "_BIN"); v != "" {
		return v, nil
	}
	if d.ExeDir != nil {
		if dir, err := d.ExeDir(); err == nil {
			if cand := filepath.Join(dir, bin); d.IsFile(cand) {
				return cand, nil
			}
			// sibling source checkout: `m` at <ws>/m-cli/dist/m → the driver at
			// <ws>/m-<engine>/dist/m-<engine> (two levels up from dist/).
			if sib := filepath.Join(dir, "..", "..", bin, "dist", bin); d.IsFile(sib) {
				return sib, nil
			}
		}
	}
	if p, err := d.LookPath(bin); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("driver binary %q not found — set M_%s_BIN, put it next to `m`, build it in ../%s/dist/, or add it to PATH",
		bin, strings.ToUpper(engine), bin)
}

// DefaultLocateDeps binds Locate to the real environment.
func DefaultLocateDeps() LocateDeps {
	return LocateDeps{
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		ExeDir: func() (string, error) {
			exe, err := os.Executable()
			if err != nil {
				return "", err
			}
			return filepath.Dir(exe), nil
		},
		IsFile: func(p string) bool {
			info, err := os.Stat(p)
			return err == nil && !info.IsDir()
		},
	}
}
