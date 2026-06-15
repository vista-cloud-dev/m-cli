---
name: arch-g2-forbidden-symbol
description: m arch check gained the full waterline gate suite G2/G3/G4 (forbidden-symbol, transport-monopoly, seam-pin) on top of G1
metadata:
  type: project
---

`m arch check` (internal/arch) now runs **G2 — forbidden-symbol** alongside G1
(dependency-direction). G2 asserts an `m`-layer repo's `.m` **code** references
no VistA-only symbol; a `v`-layer repo passes trivially (VistA is allowed above
the waterline). Branch `phase-b-arch-gates` (off m-cli main); Phase B item 2 of
the VSL effort.

**Deny-list (`vistaSymbols` in arch.go):** `^DIC/^DIE/^DIK/^DIQ` (FileMan API),
`^DD(`, `^DPT(`, `^VA(`, `^XUS*` (Kernel security), `^XPD*` (KIDS).

**Two non-obvious design points:**
- **Comment-aware.** A naive grep false-positives on STDMOCK doc lines like
  `; doc: @example do register^STDMOCK("EN^DIE",...)`. G2 scans only
  `codePortion(line)` — everything before the first `;` that is not inside a
  `"..."` string (the `"` toggle handles doubled-quote escapes). Comment
  mentions are not references.
- **Trailing-delimiter guard, not lookahead.** Go's RE2 has **no lookahead**, so
  to stop `^DIE` matching the test routine `^DIETST`, the FileMan-API pattern is
  `\^DI[CEKQ](?:[^A-Za-z0-9]|$)` — the symbol must be followed by a non-alnum or
  end-of-line.

**Implementation:** extracted a shared `forEachMLine(root, fn)` walk (skips
dist/vendor/.git/node_modules) used by both `CheckMRefs` (G1, scans the full
line) and the new `CheckVistaSymbols` (G2, scans `codePortion`). G1 is left
comment-UNAWARE deliberately (unchanged, shipped) — so **`^VSL*` named in any
m-layer `.m` comment still trips G1**; keep VSL names out of m-stdlib comments.

**Verified end-to-end:** cleaned m-stdlib (`stdseed-engine-neutral-g2`) → G2
clean; m-stdlib `master` (still has `do FILE^DIE`) → G2 flags exactly
`src/STDSEED.m:218`; all 5 m-layer repos (m-cli/m-stdlib/m-driver-sdk/m-ydb/m-iris)
G2-clean. arch pkg 88.2% cover; golangci-lint + gofmt clean.

## G3 + G4 (added same branch, 2026-06-14)

`Check` was restructured: **G1/G2 run for the m layer only; G3/G4 run for every
repo** (a `v` consumer also must not hand-roll transport / must seam-pin). Layer
resolution feeds the new `goModulePath(root)`.

- **G3 — transport-monopoly** (`CheckDriverMonopoly`): flags a non-SDK repo that
  **execs** a driver binary. **Key subtlety:** a bare driver-literal scan
  false-positives on the gate's OWN `driverBinaries` deny-list var and on test
  fixtures — so G3 requires the driver literal (`"m-ydb"`/`"m-iris"`) to
  **co-occur with `exec.Command` on the same code line** (ADR §3.2 wording).
  That makes the gate self-hosting: m-cli passes its own G3 even though arch.go
  names both binaries. `goCodePortion` strips Go `//` comments (string-aware,
  honors `\` escapes). The SDK is exempt (Check skips G3 when the module path is
  `m-driver-sdk`); a driver may exec itself (selfName exemption).
- **G4 — seam-pin** (`CheckSeamPin`): text-parses `go.mod` (no `x/mod` dep — kept
  the graph minimal). Flags a `replace` to m-driver-sdk (`seam-replace`) or a
  pseudo-version require (`seam-untagged`, matched by `\d{14}-[0-9a-f]{12}`). A
  repo not requiring the SDK passes trivially. Current state: all SDK consumers
  pin a tag (m-ydb v0.2.0, rest v0.3.0), no `replace` → all clean.

**Verified:** all 8 ecosystem repos G1–G4 clean (no false-positives); planted
exec + pseudo-version + replace fixtures red (unit tests). arch 86.7% cover,
golangci-lint + gofmt clean, m-cli self-`arch check` clean.

## Item 1 — meta-schema validation (added same branch, 2026-06-15)

`m arch check` now also validates the repo's meta artifact. `metaCandidates`
(layer resolution) flipped to **root-first**: `repo.meta.json`, then
`dist/repo.meta.json`, then `dist/v-contract.json`. New `LoadMeta(root)` reads
the repo.meta.json-shaped artifact (root preferred, then `dist/`; **not**
`v-contract.json` — different shape) → `(Meta, path, found, err)`. `ValidateMeta`
requires **id, layer, language, verification_commands** (layer ∈ m|v); consumes/
exposes optional. `Check` validates **only when a repo.meta.json is found** (a
v-contract-only / `--layer` repo is skipped — avoids breaking the v-contract test
fixtures and the pre-migration dist-only repos). Problems surface as
`Gate:"META", Kind:"meta-shape"` violations; `Report.CheckedMeta`.

**GOTCHA (real bug the all-8 sweep caught):** `consumes`/`exposes` in the live
metas are **objects, not `[]string`** (e.g. v-pkg/v-cli `exposes`, m-stdlib
`consumes`/`exposes`). Typing them as `[]string` in the `Meta` struct made
`json.Unmarshal` hard-error on 3 repos. Fix: the `Meta` struct carries **only the
4 required fields**; optional/descriptive fields (consumes, exposes, repo, role,
license, …) are unknown-and-ignored. Regression test
`TestLoadMetaIgnoresObjectOptionalFields`.

**Verified:** all 8 repos clean under G1–G4 + meta. arch 87.3% cover.

**Still owed in Phase B:** **migrate m-stdlib + v-stdlib off `dist/repo.meta.json`
to root `repo.meta.json`** (the only two not on root meta — tooling is now
root-first, so they can migrate cleanly; per-repo increments in those repos), the
scheduled meta-gate, the reusable `m-ci.yml`, and pinning `m-cli-ref` to a tag.
See the org docs-repo `docs/vsl-msl/vsl-implementation-tracker.md` Phase B row.
