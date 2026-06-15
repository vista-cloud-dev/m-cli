# m-cli memory index

- [chset byte mode](chset-byte-mode.md) — `--chset m|utf-8` on test/coverage/watch; m-stdlib byte suites need `m` on YDB
- [arch G2 forbidden-symbol](arch-g2-forbidden-symbol.md) — `m arch check` gained **G2** (no VistA symbols below the waterline): comment-aware deny-list scan (`^DIC/DIE/DIK/DIQ`, `^DD(`, `^DPT(`, `^VA(`, `^XUS*`, `^XPD*`) of m-layer `.m` code; RE2 trailing-guard (no lookahead) avoids `^DIETST`. Shared `forEachMLine` walk. Verified all 5 m-repos clean. G3/G4 still owed.
