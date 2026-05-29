package lint

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vista-cloud-dev/m-cli/internal/config"
	"github.com/vista-cloud-dev/m-cli/internal/flow"
)

// Thresholds are the resolved numeric limits the metric rules enforce. Only the
// four the Go linter wires today live here; the config layer validates the full
// Python threshold set but silently ignores keys no rule consumes yet.
type Thresholds struct {
	LineLength    int // M-MOD-001
	DotBlockDepth int // M-MOD-007
	ArgumentCount int // M-MOD-008
	CommandsLine  int // M-MOD-009
}

// DefaultThresholds mirrors the Python defaults (lint/thresholds.py).
func DefaultThresholds() Thresholds {
	return Thresholds{LineLength: 200, DotBlockDepth: 5, ArgumentCount: 7, CommandsLine: 3}
}

// Options carries the resolved, config-derived inputs baked into rule
// construction. NewLinter takes the rule set built from these, so Lint's own
// signature stays unchanged — the config flows in at build time, not call time.
type Options struct {
	Thresholds        Thresholds
	Taint             flow.TaintConfig
	KernelLocals      map[string]bool     // M-MOD-024 allowlist; nil ⇒ strict (no allowlist)
	SeverityOverrides map[string]Severity // [lint.severity]; re-stamps Rule.Severity at build
	Disable           map[string]bool     // [lint] disable; rule ids dropped after selection
}

// DefaultOptions is the built-in configuration when no config file is present.
// Faithful to the Python tool: the M-MOD-024 Kernel allowlist is OFF by default
// (KernelLocals is nil ⇒ strict) — opt in via [lint.vista] kernel_locals; taint
// treats formals as attack surface (flow.DefaultTaintConfig). With no config,
// DefaultOptions reproduces the previous hard-coded behavior except for the
// now-faithful strict-by-default Kernel allowlist.
func DefaultOptions() Options {
	return Options{
		Thresholds:   DefaultThresholds(),
		Taint:        flow.DefaultTaintConfig(),
		KernelLocals: nil,
	}
}

// OptionsFromConfig resolves a loaded project config into rule-build Options,
// layering config values over DefaultOptions. Validation already happened in the
// config package, so the only error path is an internally-inconsistent config
// (none today). Layering: thresholds merge over the defaults; taint formals/
// sanitizers override the built-in TaintConfig; [lint.vista] kernel_locals
// selects strict / built-in / explicit; severity + disable carry through.
func OptionsFromConfig(cfg config.Config) Options {
	opts := DefaultOptions()

	// Thresholds: start from defaults, apply the (validated) user overrides for
	// the keys this linter actually wires.
	if v, ok := cfg.LintThresholds["line_length"]; ok {
		opts.Thresholds.LineLength = v
	}
	if v, ok := cfg.LintThresholds["dot_block_depth"]; ok {
		opts.Thresholds.DotBlockDepth = v
	}
	if v, ok := cfg.LintThresholds["argument_count"]; ok {
		opts.Thresholds.ArgumentCount = v
	}
	if v, ok := cfg.LintThresholds["commands_per_line"]; ok {
		opts.Thresholds.CommandsLine = v
	}

	// [lint.taint]: formals_tainted nil ⇒ keep the built-in default (true);
	// extra_sanitizers add to the built-in sanitizer set.
	if cfg.LintTaintFormalsTainted != nil {
		opts.Taint.FormalsTainted = *cfg.LintTaintFormalsTainted
	}
	for _, s := range cfg.LintTaintExtraSanitizers {
		if opts.Taint.Sanitizers == nil {
			opts.Taint.Sanitizers = map[string]bool{}
		}
		opts.Taint.Sanitizers[s] = true
	}

	// [lint.vista] kernel_locals: {"default"} ⇒ the built-in allowlist; an
	// explicit list ⇒ exactly those names; absent/empty ⇒ nil (strict).
	if len(cfg.LintVistaKernelLocals) == 1 && cfg.LintVistaKernelLocals[0] == "default" {
		opts.KernelLocals = DefaultKernelLocals()
	} else if len(cfg.LintVistaKernelLocals) > 0 {
		m := make(map[string]bool, len(cfg.LintVistaKernelLocals))
		for _, n := range cfg.LintVistaKernelLocals {
			m[n] = true
		}
		opts.KernelLocals = m
	}

	if len(cfg.LintSeverityOverrides) > 0 {
		opts.SeverityOverrides = map[string]Severity{}
		for id, name := range cfg.LintSeverityOverrides {
			opts.SeverityOverrides[id] = Severity(name)
		}
	}
	if len(cfg.LintDisable) > 0 {
		opts.Disable = map[string]bool{}
		for _, id := range cfg.LintDisable {
			opts.Disable[id] = true
		}
	}
	return opts
}

// Resolve turns a rule filter (a profile name, or a comma-list mixing profile
// names and rule IDs) plus build Options into the final rule set: profile/ID
// selection, then [lint] disable drops rules, then [lint.severity] re-stamps
// Rule.Severity. Faithful to the Python select_rules + post-selection layering.
func Resolve(filter string, opts Options) ([]Rule, error) {
	rules, err := selectRules(filter, opts)
	if err != nil {
		return nil, err
	}
	if len(opts.Disable) > 0 {
		kept := rules[:0]
		for _, r := range rules {
			if !opts.Disable[r.ID] {
				kept = append(kept, r)
			}
		}
		rules = kept
	}
	if len(opts.SeverityOverrides) > 0 {
		for i := range rules {
			if s, ok := opts.SeverityOverrides[rules[i].ID]; ok {
				rules[i].Severity = s
			}
		}
	}
	return rules, nil
}

// selectRules resolves the filter against the existing Go profile names and rule
// IDs. A single token with no comma and no "M-" prefix must be a profile name; a
// comma list (or a lone rule ID) resolves each token as a profile first, then as
// a rule ID, unioned and deduped. Unknown tokens are an error listing the known
// profiles (faithful to the Python tool).
func selectRules(filter string, opts Options) ([]Rule, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		filter = "default"
	}
	if !strings.Contains(filter, ",") && !strings.HasPrefix(filter, "M-") {
		if !isProfileName(filter) {
			return nil, fmt.Errorf("unknown profile %q (known profiles: %s)", filter, strings.Join(Profiles, ", "))
		}
		return ProfileWith(filter, opts), nil
	}

	all := AllWith(opts)
	byID := make(map[string]Rule, len(all))
	for _, r := range all {
		byID[r.ID] = r
	}
	selected := map[string]Rule{}
	var unknown []string
	for _, tok := range strings.Split(filter, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if isProfileName(tok) {
			for _, r := range ProfileWith(tok, opts) {
				selected[r.ID] = r
			}
		} else if r, ok := byID[tok]; ok {
			selected[tok] = r
		} else {
			unknown = append(unknown, tok)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("unknown profile / rule id(s): %v (known profiles: %s; or use M-MOD-NN / M-STY-NN ids)",
			unknown, strings.Join(Profiles, ", "))
	}
	ids := make([]string, 0, len(selected))
	for id := range selected {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Rule, 0, len(ids))
	for _, id := range ids {
		out = append(out, selected[id])
	}
	return out, nil
}

func isProfileName(name string) bool {
	for _, p := range Profiles {
		if p == name {
			return true
		}
	}
	return false
}
