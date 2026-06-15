---
name: arch-g2-forbidden-symbol
description: m arch check gained G2 (no VistA symbols below the waterline) — comment-aware deny-list scan of m-layer .m source
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

**Still owed in Phase B:** G3 (transport-monopoly — only m-driver-sdk runs a
driver / builds the envelope), G4 (seam-pin — tagged SDK, no `replace`), the
root-`repo.meta.json` schema validation (item 1), the scheduled meta-gate, the
reusable `m-ci.yml`, and pinning `m-cli-ref` to a tag. See the org docs-repo
`docs/vsl-msl/vsl-implementation-tracker.md` Phase B row.
