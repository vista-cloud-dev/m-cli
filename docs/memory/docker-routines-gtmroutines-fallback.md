---
name: docker-routines-gtmroutines-fallback
description: Fixed (2026-06-16) — `m test --docker` dropped a GT.M VistA's resident routines (set ydb_routines=stageDir-only, which overrides gtmroutines), so VistA-dependent suites faulted 0/0. DockerRunner now falls back to ${ydb_routines:-$gtmroutines}. Unblocks VSL T1.2 / all VistA-dependent VSL*TST.
metadata:
  type: project
---

# `m test --docker` must layer the engine's resident routine base (the gtmroutines fallback)

**Bug.** `internal/engine/docker.go` `DockerRunner` ran the suite via
`docker exec -i <c> bash -lc 'export ydb_routines="<stageDir> $ydb_routines"; …'`.
For the bare **m-test-engine** (a YDB image, sets `ydb_routines`) this is correct.
But a **GT.M-configured VistA** — the FOIA **`vehu`** image — sets **`gtmroutines`**
(GT.M name), NOT `ydb_routines`. So the ambient `$ydb_routines` is empty and the
export became `ydb_routines="<stageDir> "` (staged dir only). GT.M V7.0 honors
`ydb_routines` over `gtmroutines` once it is set, so **vehu's resident VistA
routines (XPAR, FileMan, XLFDT, …) vanished from the path** — any VistA-dependent
suite faulted before `report^STDASSERT` and the runner showed **0/0**.

Globals were unaffected: docker.go never sets `ydb_gbldir`, so the ambient
`gtmgbldir` survives — which is why the gbldir half worked and only routines broke.

**Fix (`dockerEnvPrefix`, ~6 lines).** Prepend the staged dir, base falling back to
`$gtmroutines` when `$ydb_routines` is unset:
`export ydb_routines="<stageDir> ${ydb_routines:-$gtmroutines}"; `
- bare YDB (m-test-engine): `ydb_routines` set → fallback unused → unchanged.
- GT.M VistA (vehu): `ydb_routines` empty → base = `$gtmroutines` → staged AND
  resident routines both resolve.
Extracted to the pure helper `dockerEnvPrefix(stageDir)` for a table test
(`docker_test.go`, TDD red→green).

**This is the m-cli (DockerEngine) analog of the m-ydb `$ZGBLDIR` fix**
([[../../../m-ydb/docs/memory/m-ydb-docker-gbldir]]). NOTE the path split that made
this subtle: `m test --docker` uses **m-cli's internal DockerEngine**
(`internal/engine/docker.go` + `ydb.go`), whereas `m vista exec` / `v pkg` use the
**m-ydb driver** (`mdriver.Client`). The m-ydb gbldir/routines env knobs
(`M_YDB_GBLDIR`/`M_YDB_ROUTINES`) apply to the *driver* path, NOT to `m test`.
After this fix `m test --docker vehu` needs **no** `M_YDB_*` host vars — the
container's `bash -l` env supplies `gtmgbldir` + `gtmroutines`.

**Validated (the acceptance gate).** v-stdlib `VSLCFGTST` (XPAR config adapter) —
`0/0` before → **3/3 GREEN after** on BOTH engines via the driver stack:
`m test --engine ydb --docker vehu --chset m …` and
`m test --engine iris --docker foia-t12 --namespace VISTA …`. IRIS needed no change
(routines are namespace-resident). m-test-engine regression green (2/2, fallback
unused). `make lint` + `go test ./...` clean. Branch `docker-routines-base`.

**Why it matters:** unblocks **all** VistA-dependent `VSL*TST` testing over
`m test --docker`, not just VSLCFG — the M1 walking skeleton (T1.2→T1.5) and beyond.
