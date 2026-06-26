---
name: clikit-shared-module-migration
description: m-cli migrated off its vendored m-cli/clikit/ copy to the shared github.com/vista-cloud-dev/clikit v0.2.0 module (2026-06-26), gaining the new styled grouped help; CLI commands now carry group:"" tags.
metadata:
  type: project
---

**m-cli migrated to the shared clikit module (2026-06-26).** m-cli previously
**vendored** clikit as an in-repo subpackage `m-cli/clikit/` (imported as
`github.com/vista-cloud-dev/m-cli/clikit`). It now consumes the standalone
`github.com/vista-cloud-dev/clikit v0.2.0` like v-cli/v-pkg do — killing the fork
and picking up clikit's new styled, curated, grouped help renderer + pager (see
clikit's `cli-discovery-ux`).

**What the migration was (mechanical, zero functional change):**
- The vendored copy diverged from the shared module **only in doc comments**
  (6 of 8 files byte-identical; `globals.go`/`version.go` differed in package
  blurb / one ldflags example) — so deleting it was safe.
- Rewrote the import path `…/m-cli/clikit` → `…/clikit` in **6 files**: `main.go`,
  `vista_cmd.go`, `main_test.go`, `internal/dispatch/dispatch.go` + its test,
  `internal/arch/arch_test.go`.
- `git rm -r clikit/`; `go get github.com/vista-cloud-dev/clikit@v0.2.0`;
  `go mod tidy`.
- Added `group:""` tags to the `CLI` struct → editorial help categories:
  **Author** (fmt/lint/lsp/watch), **Quality** (test/coverage/arch),
  **Engine** (vista), **Sync** (list/pull/status/verify/push/kids),
  **Introspect** (version/schema); `install-completions` untagged → "Commands".

**Gotcha — fetching a new clikit version airgapped:** the house default
`GOPROXY=file://…cache/download GOSUMDB=off` could NOT fetch v0.2.0 (not yet in
the file cache), and forcing `GOPROXY=direct GOSUMDB=off` **failed verifying the
go1.26.3 toolchain module** ("checksum database disabled by GOSUMDB=off"). The fix
was simply to use the **default env** (`GOPROXY=proxy.golang.org,direct`,
`GOSUMDB=sum.golang.org`) — the machine has normal network — which fetched
clikit@v0.2.0 cleanly and populated the cache. Don't reach for `direct`+`GOSUMDB=off`
when adding a brand-new dependency version; it breaks toolchain verification.

**Gates:** full `go test -race ./...` green (incl. the ~5-min flow/lint engine
suites); vet/gofmt clean; `m help` smoke-tested showing the grouped surface.

**Phase 2 — `m explore` (2026-06-26).** Repinned clikit v0.2.0 → v0.3.2 and
mounted `Explore clikit.ExploreCmd` in the root CLI struct (Introspect group) →
`m explore` opens the interactive command palette; non-TTY falls back to full
help. Build/vet/root-test green; smoke-tested mounting + fallback.
