# m-cli

**The cross-engine M toolchain — the `m` busybox.** A single static Go binary
that delivers `fmt`/`lint`/`lsp`/`test`/`coverage`/`watch` for M (MUMPS) source,
working across **YottaDB and IRIS** (spec §1). It's the Go rewrite of the Python
`m-cli`, built on the [`m-parse`](https://github.com/vista-cloud-dev/m-parse)
substrate (tree-sitter-m via wazero — no CGO).

> **Status: early.** This stage ships **`m fmt`** (the AST-preserving
> formatter). `lint`/`lsp`/`test`/`coverage`/`watch` land in subsequent stages
> per the [implementation plan](https://github.com/vista-cloud-dev/docs).

```sh
m fmt routine.m                      # report whether it needs formatting
m fmt --rules=canonical --write .    # uppercase command keywords in place
m fmt --rules=canonical --check .    # CI gate: exit 3 if any file differs
cat routine.m | m fmt --rules=canonical --stdin
m version        # version + Go toolchain + embedded grammar hash
m schema | jq .  # the command/flag tree as JSON (agent discovery)
```

---

## `m fmt`

An **AST-preserving** formatter over the `m-parse` syntax tree (spec §3.1). It
works as *edits-over-source*: rules emit byte-span edits guided by the parse
tree, applied to the original bytes. Two key properties:

- **Identity by default.** With the default `--rules=identity`, nothing changes —
  unformatted input round-trips byte-for-byte. Formatting is **opt-in** (mirrors
  the Python `m-cli`'s identity default + canonical layer).
- **AST-preserving.** Rules only change what they must (e.g. keyword letter-case),
  verified by an internal tree-shape check: `parse(format(src))` has the same
  shape as `parse(src)`.

| Preset (`--rules`) | What it does |
|---|---|
| `identity` (default) | No-op; the round-trip baseline. |
| `canonical` | `uppercase-command-keywords` — `set`→`SET`, `w`→`W`, leaving arguments untouched. (More rules + the `pythonic`/`pythonic-lower`/`compact`/`sac` presets follow.) |

**Flags:** `--check` (report files needing formatting; **exit 3** if any — the CI
gate), `--write`/`-w` (rewrite in place), `--stdin` (format stdin → stdout as a
raw filter). With no flags it reports what *would* change (exit 0).

**File discovery** walks paths (default `.`) for **`.m` / `.mac` / `.int`** —
`.int` is included because VistA loaded via `^%RI` stores its routine source as
`.int` (there `.int` *is* the source, not compiler output). Explicit file
arguments are formatted as given.

## Architecture

```
   m fmt … ──► discover .m/.mac/.int ──► for each file:
                                           parse (m-parse: tree-sitter-m via
                                                  wazero, embedded WASM, no CGO)
                                           rules emit byte-span edits ──► apply
                                           ──► --check (exit 3) / --write / report
```

The whole binary stays **static (`CGO_ENABLED=0`)** because the parser is the
embedded grammar WASM run in wazero (`m-parse`), not a CGO tree-sitter binding.

## Repository layout

```
m-cli/
├── main.go                 # the `m` CLI grammar (Kong struct) + `m fmt`, version, schema
├── internal/mfmt/          # the formatter
│   ├── format.go           #   Format() + the edits-over-source engine (applyEdits)
│   ├── rules.go            #   Rule interface; identity/canonical presets; uppercase-command-keywords
│   └── shape.go            #   SameShape — the AST-preserving check
├── clikit/                 # shared CLI conventions (from go-cli-template)
├── Makefile · .golangci.yml · .github/workflows/ci.yml
└── LICENSE · NOTICE        # Apache-2.0 (Go); see Licensing
```

## Build, test, CI

| Target | What it does |
|--------|--------------|
| `make build` | `dist/m`, static (`CGO_ENABLED=0`), `-trimpath`, version-stamped |
| `make test`  | `go test -race -cover ./...` (race needs CGO; the rest is CGO-free) |
| `make lint`  | `golangci-lint run ./...` |
| `make schema`| build + emit the JSON schema (CI conformance artifact) |
| `make dist`  | cross-compile `linux/{amd64,arm64}`, `darwin/arm64`, `windows/amd64` |

CI (the org's reusable `go-ci` workflow) runs golangci-lint, race tests, the
`schema` contract, and a static `CGO_ENABLED=0` cross-compile matrix.

## Licensing

The Go code here is **Apache-2.0** (`LICENSE`/`NOTICE`). The binary links
`m-parse`, whose embedded grammar WASM is currently **AGPL-3.0**, so a built `m`
transitively includes an AGPL-derived artifact. **Per project policy, all
licensing reconciliation is deferred to project completion** — end-state
Apache-2.0 for every artifact except the VS Code extensions (MIT). The interim
AGPL status is not a blocker. See the `m-parse` `NOTICE` and the toolchain spec.
