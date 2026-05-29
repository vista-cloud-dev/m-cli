// Package config loads m-cli's project-level configuration — the [lint.*] and
// [fmt] sections of a `.m-cli.toml` (preferred) or a `[tool.m-cli]` table in
// pyproject.toml. It is a faithful port of the Python tool's config.py.
//
// Discovery walks UP from a start directory toward the filesystem root or the
// nearest `.git` boundary (whichever comes first), mirroring how ruff / black
// scope a project. Settings layer defaults → config file → CLI flag, with flags
// always winning; that final layering happens at the call sites (main.go), not
// here. An absent file yields an empty Config and changes nothing.
//
// Unknown keys are silently ignored (cheap forward-compat). A value that IS
// present but invalid (a bad severity name, a non-int threshold, an unknown
// threshold key, a bad target_engine, …) is a HARD error naming the source path
// — never a silent fallback.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

const (
	// ConfigFilename is the preferred project-local config file.
	ConfigFilename = ".m-cli.toml"
	// PyprojectFilename is the Python-packaging fallback.
	PyprojectFilename = "pyproject.toml"
	// pyprojectTable is the table m-cli config nests under in pyproject.toml.
	pyprojectTable = "m-cli"
)

// KnownEngines are the recognized values for [lint] target_engine. "any"
// (default) keeps the linter portable; the named engines unlock engine-specific
// allowlists.
var KnownEngines = []string{"any", "yottadb", "iris"}

// KnownThresholds maps every configurable threshold name to its default value.
// It doubles as the allowlist for config-file validation — an unknown key is a
// likely typo and is rejected at load time. Ported verbatim from the Python
// tool's lint/thresholds.py (the Go linter only wires a subset today, but the
// full set validates so a forward-compatible config never errors spuriously).
var KnownThresholds = map[string]int{
	"line_length":         200,
	"code_line_length":    1000,
	"routine_lines":       1000,
	"label_lines":         50,
	"cyclomatic":          15,
	"cognitive":           20,
	"dot_block_depth":     5,
	"argument_count":      7,
	"commands_per_line":   3,
	"comment_density_pct": 10,
}

// knownSeverities is the set of valid [lint.severity] values.
var knownSeverities = map[string]bool{
	"error": true, "warning": true, "style": true, "info": true,
}

// Config is the resolved m-cli configuration. A zero value means "nothing
// configured"; every CLI / linter falls back to its built-in default for any
// unset field.
type Config struct {
	LintRules                string            // [lint] rules — profile name or comma-list ("" = unset)
	LintDisable              []string          // [lint] disable — rule ids skipped after selection
	LintSeverityOverrides    map[string]string // [lint.severity] — rule id -> severity name
	LintTargetEngine         string            // [lint] target_engine — normalized; "" = unset
	LintThresholds           map[string]int    // [lint.thresholds] — user overrides only (validated)
	LintTaintFormalsTainted  *bool             // [lint.taint] formals_tainted — nil = use built-in default
	LintTaintExtraSanitizers []string          // [lint.taint] extra_sanitizers — upper-cased
	LintVistaKernelLocals    []string          // [lint.vista] kernel_locals — {"default"} | names | empty (strict)
	LintVistaTrustedRoutines []string          // [lint.vista] trusted_routines — same shape
	FmtRules                 string            // [fmt] rules — "" = unset
	SourcePath               string            // where this Config was loaded from, if any
}

// FindConfig walks up from start looking for an m-cli config file, returning
// the first match's path or "" if none. A .m-cli.toml is preferred over a
// pyproject.toml at the same level. The walk stops at the filesystem root or at
// a directory containing .git (whichever comes first) — but the per-level
// config check runs BEFORE the boundary check, so a config sitting at the .git
// directory is still found.
func FindConfig(start string) string {
	current := start
	if abs, err := filepath.Abs(start); err == nil {
		current = abs
	}
	if fi, err := os.Stat(current); err == nil && !fi.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		local := filepath.Join(current, ConfigFilename)
		if isFile(local) {
			return local
		}
		py := filepath.Join(current, PyprojectFilename)
		if isFile(py) && pyprojectHasMCLITable(py) {
			return py
		}
		if exists(filepath.Join(current, ".git")) {
			return ""
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

// LoadConfig finds and loads the config starting from start. Returns an empty
// Config (no error) when no file is found or the file is unreadable; returns an
// error only when a found file is malformed or contains an invalid value.
func LoadConfig(start string) (Config, error) {
	path := FindConfig(start)
	if path == "" {
		return Config{}, nil
	}
	return LoadFile(path)
}

// LoadFile loads and validates a specific config file (used for an explicit
// --config flag, bypassing discovery). An unreadable file yields an empty
// Config; a malformed/invalid one is an error.
func LoadFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, nil
	}
	var parsed map[string]any
	if _, err := toml.Decode(string(data), &parsed); err != nil {
		return Config{}, fmt.Errorf("%s: invalid TOML: %w", path, err)
	}
	section := parsed
	if filepath.Base(path) == PyprojectFilename {
		section = asTable(asTable(parsed["tool"])[pyprojectTable])
	}
	return fromDict(section, path)
}

func pyprojectHasMCLITable(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var parsed map[string]any
	if _, err := toml.Decode(string(data), &parsed); err != nil {
		return false
	}
	_, ok := asTable(parsed["tool"])[pyprojectTable]
	return ok
}

// fromDict validates a parsed config section into a Config. source names the
// file for error messages.
func fromDict(section map[string]any, source string) (Config, error) {
	lint := asTable(section["lint"])
	fmtSec := asTable(section["fmt"])

	cfg := Config{SourcePath: source}
	if s, ok := lint["rules"].(string); ok {
		cfg.LintRules = s
	}
	if s, ok := fmtSec["rules"].(string); ok {
		cfg.FmtRules = s
	}

	// [lint] disable — list of rule ids.
	if raw, ok := lint["disable"]; ok && raw != nil {
		list, ok := raw.([]any)
		if !ok {
			return Config{}, fmt.Errorf("%s: [lint] disable must be a list of rule ids, got %s",
				source, tomlType(raw))
		}
		for _, item := range list {
			if s, ok := item.(string); ok && s != "" {
				cfg.LintDisable = append(cfg.LintDisable, s)
			}
		}
	}

	// [lint.severity] — rule id -> severity name.
	if raw, ok := lint["severity"]; ok && raw != nil {
		tbl, ok := raw.(map[string]any)
		if !ok {
			return Config{}, fmt.Errorf("%s: [lint.severity] must be a table mapping rule id -> severity", source)
		}
		cfg.LintSeverityOverrides = map[string]string{}
		for ruleID, v := range tbl {
			name, ok := v.(string)
			if !ok {
				return Config{}, fmt.Errorf("%s: [lint.severity] %q: expected a string, got %s",
					source, ruleID, tomlType(v))
			}
			norm := strings.ToLower(strings.TrimSpace(name))
			if !knownSeverities[norm] {
				return Config{}, fmt.Errorf("%s: [lint.severity] %q: unknown severity %q "+
					"(expected one of [error info style warning])", source, ruleID, name)
			}
			cfg.LintSeverityOverrides[ruleID] = norm
		}
	}

	// [lint.thresholds] — name -> positive integer, validated against KnownThresholds.
	if raw, ok := lint["thresholds"]; ok && raw != nil {
		tbl, ok := raw.(map[string]any)
		if !ok {
			return Config{}, fmt.Errorf("%s: [lint.thresholds] must be a table mapping name -> "+
				"positive integer, got %s", source, tomlType(raw))
		}
		thresholds, err := validateThresholds(tbl)
		if err != nil {
			return Config{}, fmt.Errorf("%s: [lint.thresholds]: %w", source, err)
		}
		cfg.LintThresholds = thresholds
	}

	// [lint] target_engine.
	if raw, ok := lint["target_engine"]; ok && raw != nil {
		s, ok := raw.(string)
		if !ok {
			return Config{}, fmt.Errorf("%s: [lint] target_engine must be a string, got %s",
				source, tomlType(raw))
		}
		norm := strings.ToLower(strings.TrimSpace(s))
		if !knownEngine(norm) {
			return Config{}, fmt.Errorf("%s: [lint] target_engine %q: unknown engine "+
				"(expected one of %v)", source, s, KnownEngines)
		}
		cfg.LintTargetEngine = norm
	}

	// [lint.taint].
	if raw, ok := lint["taint"]; ok && raw != nil {
		tbl, ok := raw.(map[string]any)
		if !ok {
			return Config{}, fmt.Errorf("%s: [lint.taint] must be a table, got %s", source, tomlType(raw))
		}
		if ft, ok := tbl["formals_tainted"]; ok {
			b, ok := ft.(bool)
			if !ok {
				return Config{}, fmt.Errorf("%s: [lint.taint] formals_tainted must be a boolean, got %s",
					source, tomlType(ft))
			}
			cfg.LintTaintFormalsTainted = &b
		}
		if es, ok := tbl["extra_sanitizers"]; ok && es != nil {
			list, ok := es.([]any)
			if !ok {
				return Config{}, fmt.Errorf("%s: [lint.taint] extra_sanitizers must be a list of "+
					"intrinsic-keyword strings, got %s", source, tomlType(es))
			}
			for _, item := range list {
				if s, ok := item.(string); ok && s != "" {
					cfg.LintTaintExtraSanitizers = append(cfg.LintTaintExtraSanitizers,
						strings.ToUpper(strings.TrimSpace(s)))
				}
			}
		}
	}

	// [lint.vista].
	if raw, ok := lint["vista"]; ok && raw != nil {
		tbl, ok := raw.(map[string]any)
		if !ok {
			return Config{}, fmt.Errorf("%s: [lint.vista] must be a table, got %s", source, tomlType(raw))
		}
		kl, err := allowlist(tbl["kernel_locals"], source, "kernel_locals")
		if err != nil {
			return Config{}, err
		}
		cfg.LintVistaKernelLocals = kl
		tr, err := allowlist(tbl["trusted_routines"], source, "trusted_routines")
		if err != nil {
			return Config{}, err
		}
		cfg.LintVistaTrustedRoutines = tr
	}

	return cfg, nil
}

// allowlist parses a [lint.vista] allowlist value: the string "default"
// (→ {"default"}), a list of names, or absent/empty (→ nil, strict). Any other
// shape is an error.
func allowlist(raw any, source, key string) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case string:
		if strings.ToLower(strings.TrimSpace(v)) == "default" {
			return []string{"default"}, nil
		}
		return nil, fmt.Errorf("%s: [lint.vista] %s string must be \"default\", got %q", source, key, v)
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%s: [lint.vista] %s must be \"default\" or a list of names, got %s",
			source, key, tomlType(raw))
	}
}

// validateThresholds checks each override against KnownThresholds (key must be
// known, value a positive integer) and returns the user overrides as ints.
func validateThresholds(overrides map[string]any) (map[string]int, error) {
	out := map[string]int{}
	for key, val := range overrides {
		if _, ok := KnownThresholds[key]; !ok {
			return nil, fmt.Errorf("unknown threshold %q (known thresholds: %s)", key, sortedKeys(KnownThresholds))
		}
		n, ok := val.(int64)
		if !ok || n <= 0 {
			return nil, fmt.Errorf("threshold %q must be a positive integer, got %v", key, val)
		}
		out[key] = int(n)
	}
	return out, nil
}

func knownEngine(name string) bool {
	for _, e := range KnownEngines {
		if e == name {
			return true
		}
	}
	return false
}

// asTable coerces a parsed TOML value to a table, returning an empty (non-nil)
// map when it is absent or not a table — so callers can index safely.
func asTable(v any) map[string]any {
	if t, ok := v.(map[string]any); ok {
		return t
	}
	return map[string]any{}
}

func isFile(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// tomlType names a parsed value's type for error messages.
func tomlType(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case int64:
		return "integer"
	case float64:
		return "float"
	case bool:
		return "bool"
	case []any:
		return "array"
	case map[string]any:
		return "table"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func sortedKeys(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// simple insertion sort to avoid importing sort for one call
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j-1] > keys[j]; j-- {
			keys[j-1], keys[j] = keys[j], keys[j-1]
		}
	}
	return strings.Join(keys, ", ")
}
