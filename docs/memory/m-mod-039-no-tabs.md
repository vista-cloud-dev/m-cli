---
name: m-mod-039-no-tabs
description: New lint rule M-MOD-039 bans tab characters in M source (error, default profile); m fmt canonical preset auto-detabs leading whitespace. Closes the gate gap that let v-stdlib ship tab-indented routines.
metadata:
  type: project
---

Added **M-MOD-039** to m-cli `internal/lint/rules.go` (2026-06-29): flags any tab
character in M source — one finding per offending line, at the first tab. Severity
**Error**, tags `["modern","vista"]`, so it gates in the **default** profile (and
modern/vista/all). Companion auto-fix: a **`DetabLeadingWhitespace`** rule in the
`canonical` `m fmt` preset (`internal/mfmt/rules.go`) converts each tab in a line's
**leading-whitespace run** to a single space — it touches ONLY the indentation
region, so a tab inside a string literal or comment (data) is preserved and the
parse-tree shape is unchanged.

**Why it matters (the non-obvious cross-repo link).** The "no tabs / spaces only"
hygiene was documented in the `mumps-modern-style` skill (SAC + modern both ban
tabs) but **was never implemented as a rule** — neither `m lint` nor `m fmt`
enforced or fixed it. That gate gap let **v-stdlib** ship all 6 `VSL*.m` routines
with leading-TAB indentation. The downstream damage surfaced in v-pkg's adversarial
stress test: an engine **normalizes a leading TAB → a single SPACE** at install
(proven on both YDB/vehu and IRIS/foia-t12: shipped `'\t;…'` → live `' ;…'`), so the
live routine diverges from the shipped `.KID` source on every line and
`v pkg verify --drift` **false-positives** for every tab-indented routine
(`RoutineDriftMatch` byte-compares lines). m-stdlib (space-indented) was unaffected.
So a "cosmetic" indentation choice silently broke a verification gate two repos
away — exactly why the ban is now machine-enforced, not just documented.

Tag gotcha: do NOT tag a modern-track rule `"sac"`. The lint `sac` profile is the
XINDEX-derived ruleset; `TestXindexProfileMembership` asserts every `sac`-tagged
rule is also `xindex`-family. Tag broad hygiene rules `["modern","vista"]` (like
M-MOD-038), not `"sac"`.

Pairs with the v-stdlib detab (the 6 `VSL*.m` → spaces) and v-pkg's
`adversarial-stress-gate`. `make all` green (lint+test+build+arch).
