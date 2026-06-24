---
name: m-mod-038-cstyle-quote-escape
description: New lint rule M-MOD-038 catches C-style \" quote-escapes inside M string literals (error severity).
metadata:
  type: project
---

Added **M-MOD-038** to m-cli `internal/lint/` (2026-06-23): flags a C-style `\"`
quote-escape inside an M string literal. In MUMPS a `"` inside a string is escaped
by **doubling** it (`""`), never with a backslash; `\"` terminates the string and
leaves the `\` as the last content char, so the rest of the line mis-parses into
barewords — a latent compile break. The motivating real bug was v-stdlib FU-5 5B.1
(`VSLRPCWRAPTST.m`): the routine failed to load and `m test` reported a **silent
`0/0` suite abort** before `report^STDASSERT` ran, with no localization — and the
*old* `m lint` passed it clean.

Non-obvious decisions:
- **Lexical, not tree-sitter.** The offending line's parse tree is already wrong
  (the string really does terminate early), so a node-based check is unreliable.
  `cStyleQuoteEscapes(line)` lexes each line as M: string runs `"`→ next unpaired
  `"`, a doubled `""` stays inside, a `;` *outside* a string starts a comment.
- **Continuation-char heuristic avoids false positives at error severity.** A `\`
  immediately before a `"` is flagged only when that `"` terminates the string
  (not a doubled `""`) **and** is followed by a *word char* (`[A-Za-z0-9%]`) — the
  signal the author meant the string to keep going. A string whose content
  legitimately ends in a backslash (`"C:\"`, terminator followed by a delimiter /
  `_` concat / EOL) is left alone. Verified zero false positives across the
  m-stdlib JSON/regex/TOML modules (which legitimately contain `\"`), e.g.
  `STDJSON` line 454 `"bad escape '\"_esc_` (followed by `_` concat → not flagged).
- **Tagged `modern` + `vista`** so it fires under *both* dialect knobs (a `\"` is
  wrong in every dialect); stays out of `pedantic` so it lives in `default` at
  **error** severity. This broke `TestXindexProfileMembership`'s old invariant
  "every vista rule ∈ xindex" — updated it to allow a modern rule cross-tagged
  vista.
- Suppression (`; m-lint: disable=M-MOD-038`) is free via the central choke point
  in `LintNamed`.

`make all` green (lint + `go test -race -cover` + build + arch). Built `m` flags
the real repro at error severity.

Follow-up (separate, not done here): the `mumps-modern-style` SKILL.md rule table
is hand-curated (not generated from the registry) and already lags M-MOD-036/037 —
add 036/037/038 rows when next touched.
