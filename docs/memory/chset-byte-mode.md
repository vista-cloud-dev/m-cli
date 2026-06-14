---
name: chset-byte-mode
description: m test/coverage/watch take --chset m|utf-8; m-stdlib byte suites need m on YDB
metadata:
  type: reference
---

`m test`, `m coverage`, and `m watch --run` accept `--chset m|utf-8`
(threaded through `engine.Options.Chset`). Default is empty = engine default
(YDB inherits ambient `$ydb_chset`, which in the `m-test-engine` container is
UTF-8).

**Running m-stdlib byte-oriented suites via m-cli requires `--chset m`.**
STDCSPRNG/STDB64/STDHEX (and STDJSON UTF-8 decode) assume one M char == one
byte; under UTF-8 byte values >127 re-encode and the suite aborts (e.g.
`STDCSPRNGTST` reports 0/0). Verified live: `STDCSPRNGTST` is 406/406 under
`--chset m` and fails without it (exit 3) — default unchanged.

Mechanics: on YDB the adapter prepends `env ydb_chset=<M|UTF-8>` to the argv
(works for LocalRunner and DockerRunner, no Runner-seam change). On IRIS the
flag is a **no-op** — byte mode is inherent (Unicode IRIS round-trips all 256
byte values; no `ydb_chset` analog).

Landed via PR #2 (`engine-chset-byte-mode`), rebased onto post-T0.1 main
2026-06-14. The `internal/engine` adapters were unchanged by T0.1, so the only
conflict was the `testCmd` struct (both `--resident` and `--chset` kept).
Closes "Stage A" of m-stdlib's follow-up tracker (in the m-stdlib repo).
