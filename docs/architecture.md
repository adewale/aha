# Architecture

`aha` is a local-first agent-history archive. It reads supported coding-agent history roots, pushes their file versions as content-addressed blobs plus a per-machine snapshot manifest into a depot, pulls unknown blobs into a local SQLite + FTS5 corpus, and exposes stable search/read refs for humans and agents.

## System overview

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│                                   aha CLI                                    │
│ cmd/aha/main.go → internal/cli.Run / RunMain / JSON errors / command registry│
└──────────────┬──────────────────────────────┬───────────────────────────────┘
               │                              │
               ▼                              ▼
      ┌────────────────┐             ┌─────────────────────┐
      │ internal/config │             │ internal/adapters   │
      │ JSONC defaults  │             │ Pi/Claude/Codex/OpenCode │
      └───────┬────────┘             └──────────┬──────────┘
              │                                 │ read-only discovery/parse
              │                                 ▼
              │                     ┌─────────────────────────┐
              │                     │ Local agent history roots│
              │                     │ ~/.pi ~/.claude ~/.codex│
              │                     │ + OpenCode SQLite DBs   │
              │                     └──────────┬──────────────┘
              │                                │ stable copies
              ▼                                ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                              internal/archive                               │
│ Capture → stat/hash sources → diff vs parent manifest → new blobs + manifest │
└──────────────────────────────────────┬───────────────────────────────────────┘
                                       │ put blobs/manifest/pointer · get pointers/manifests
                                       ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                               internal/depot                                │
│ local: filesystem depot          r2: S3-compatible Cloudflare R2 depot       │
│ blobs/v2/<sha>.zst  machines/<id>/manifests/<sha>.json  machines/<id>/latest │
└──────────────────────────────────────┬───────────────────────────────────────┘
                                       │ pull: fetch only unknown blobs
                                       ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                              internal/corpus                                │
│ corpus.db + blobs/ + images/ + FTS5 tables; derived and rebuildable          │
└──────────────────────┬───────────────────────────────────────┬──────────────┘
                       │                                       │
                       ▼                                       ▼
             ┌──────────────────┐                    ┌──────────────────────┐
             │ internal/search  │                    │ corpus read/status   │
             │ SQLite FTS5      │                    │ refs, conflicts      │
             └──────────────────┘                    └──────────────────────┘
```

## Core nouns

| Noun | Meaning | Durable? |
|---|---|---|
| Source root | A local history directory/DB for Pi, Claude Code, Codex, or OpenCode. | External input; `aha` treats it read-only. |
| Blob | One compressed file version, stored write-once at `blobs/v2/<sha256>.zst` and addressed by the SHA-256 of its contents. | Yes. Blob bytes are durable truth. |
| Manifest | A small JSON object listing one machine's complete state — every file entry with its `blob_sha256`. Its canonical-encoding SHA-256 is the snapshot identity. | Yes. Write-once under `machines/<id>/manifests/<sha256>.json`. |
| Latest pointer | `machines/<id>/latest`, a tiny `{manifest_sha256}` pointer updated by conditional PUT. | Repointable; every manifest it ever pointed at remains. |
| Depot | Blob/manifest store addressed as `local:PATH` or `r2:BUCKET`. Append-only: it never deletes. | Yes. |
| Bundle | The v1 portable `tar.zst` file format. Survives only in `aha export` (write) and `aha ingest <bundle.tar.zst>` (import). | A bundle file is a portable copy, not a depot object. |
| Corpus | Local SQLite + FTS5 database and blob store derived from pulled snapshots. | Rebuildable. It is the query engine, not truth. |
| Ref | Stable canonical search/read identifier: `msg:v1:<b64-session>:<b64-entry>`, `session:v1:<b64-session>`, or `artifact:v1:<sha256>`. | API contract for retrieval. |

## On-disk / object layouts

### Depot

Local depot default: `~/.aha/depot`.

```text
<depot>/
  aha-depot.json                                 # marker (schema aha-depot/v2)
  blobs/v2/<sha256>.zst                          # one compressed file version, write-once
  machines/index.json                            # machine-namespace registry, conditional PUT
  machines/<machine_id>/manifests/<sha256>.json  # one snapshot manifest, write-once
  machines/<machine_id>/latest                   # pointer {manifest_sha256}, conditional PUT
```

R2 uses the same key layout inside the bucket.

The machines index exists so pull can discover machine namespaces with a single GET instead of a LIST; it is appended to (conditional PUT with retry) only on a machine's first-ever push. v1 depots (`depot.json` + `bundles/v1/` + `catalog/v1/`) are refused at `init` — there is no migration; a v1 depot is recovered by feeding its bundle files through `aha ingest <bundle.tar.zst>` and pushing fresh.

See [`depot-lifecycle.md`](depot-lifecycle.md) for the states a depot moves through and the commands that move it (`init`, `use`, `snapshot`/`refresh`, `verify`).

### Corpus

Default corpus: `~/.aha`.

```text
<corpus>/
  corpus.db
  blobs/...
  images/...
```

SQLite tables store machines, sources, snapshots (keyed by `manifest_sha256`), files, sessions, entries, messages, artefacts, images, and conflicts. FTS5 virtual tables index message text and text artefacts. Blob files preserve raw file content needed for reads. Pre-v2 corpora are rejected at open with an instruction to rebuild (delete and re-pull); the corpus is always rebuildable from the depot.

## Command flows and walkthrough

The shortest mental model is:

```text
local agent histories → push (new blobs + manifest) → depot → pull into local corpus → search/read
```

### `aha snapshot` (push)

```text
config + flags
  → adapters discover local sessions/artifacts read-only
  → GET own latest pointer + parent manifest (a few KB)
  → stat/hash local sources (advisory capture cache skips re-reading unchanged files; --force bypasses it)
  → diff against the parent manifest
  → PUT only blobs the parent doesn't list
  → PUT the new manifest, then conditional PUT the pointer
```

Snapshot is push-only: it creates durable evidence in the depot and does not touch the corpus. Unchanged state means zero writes — not even a new manifest. If OpenCode is enabled, discovery may also refresh the private OpenCode JSONL export cache before capture.

### `aha ingest` (pull / import)

With an explicit `bundle.tar.zst` path, ingest imports a portable v1-format bundle file (the compatibility hedge for pre-v2 depots and for journeys that hand someone one file). With no paths, ingest is anti-entropy pull from the configured depot:

```text
GET machines/index.json
  → GET every machine's latest pointer + manifest
  → diff each manifest against corpus-known (machine, path, sha) file versions
  → fetch only unknown blobs (read-side hash verification)
  → parse each fetched file version once
  → corpus planner/blob publisher/writer transaction
  → SQLite rows + FTS + blobs
```

Ingest builds the query corpus. It never rereads mutable source roots to decide identity; identity comes from manifest-listed content hashes.

### `aha refresh`

`refresh` is the daily path: push then pull.

```text
push new file versions + manifest for this machine
then pull unknown snapshots/blobs from every machine into the local corpus
```

A steady-state refresh with no changes is a handful of small GETs and zero PUTs, zero blob fetches, zero parses.

### `aha export`

```text
resolve a machine's latest pointer → manifest
  → fetch the manifest's blobs
  → materialize one portable v1-format bundle.tar.zst
```

`export` and `ingest <bundle.tar.zst>` are the only places the v1 tar format survives; together they preserve the "hand someone one file" journey.

### `aha search` and `aha read`

```text
search query → SQLite FTS5 → results with structured ref/ref_text
read <ref>   → corpus read APIs → transcript window or artifact body
```

Search and read never contact the depot. They are local/offline corpus operations.

### `aha verify --repair-fts`

```text
verify corpus rows/blobs/derived FTS state
optionally rebuild FTS rows from messages/artifacts
report machine-readable problems for agents and scripts
```

The corpus verifier checks rebuildable local state. It does not contact the depot; use `depot verify` for depot object integrity.

### `aha depot verify`

```text
validate aha-depot.json schema/layout
resolve every machine pointer to a well-formed manifest
--deep: fetch every referenced blob and verify its hash
```

Quick verify is pointer/manifest resolution; `--deep` is the byte-level audit (many small GETs — slower than streaming one tar, accepted as the explicit audit path; it is the only path allowed to LIST). Blobs and manifests are write-once durable truth, so there is no `--repair` mode: there is no derived catalogue to rebuild.

## Multiple snapshots, aggregation, deduplication, and efficiency

A user can push from one or many machines into the same depot. Publishing happens through `aha snapshot --depot ...` or `aha refresh --depot ...`; explicit path ingestion (`aha ingest bundle.tar.zst`) imports into the corpus and does not publish into the depot.

```text
machine A push A1 ─┐
machine A push A2 ─┼─→ shared depot blob pool + per-machine manifests → local corpus on each machine
machine B push B1 ─┘
```

Aggregation is a union over per-machine namespaces:

```text
blobs/v2/<sha…>.zst                          # shared, deduplicated file versions
machines/machine-a/manifests/<sha-a1>.json
machines/machine-a/manifests/<sha-a2>.json
machines/machine-b/manifests/<sha-b1>.json
machines/machine-a/latest                    # → A2
machines/machine-b/latest                    # → B1
machines/index.json                          # machine-a, machine-b
```

`aha depot ls`, no-argument `aha ingest`, and `status --depot` read the machines index plus each machine's latest pointer and manifest — steady-state paths never LIST objects. `status --depot` reports `depot_behind_snapshots` and `depot_machines_listed`. Each machine writes only its own namespace, so normal publishing has no cross-machine write contention; the only shared objects (`machines/index.json` and each pointer) use conditional PUT with retry.

### How deduplication works

| Layer | Key | Effect |
|---|---|---|
| Depot blob store | file content SHA-256 in `blobs/v2/<sha>.zst` | Each unique file version is stored once, ever, fleet-wide — identical content is one object by construction. |
| Push diff baseline | parent manifest's file list | Push uploads only file versions the parent manifest doesn't list; unchanged state means zero writes. |
| Capture cache | `(path, size, mtime_ns, inode) → sha256` | Push skips re-reading/re-hashing unchanged files. Advisory only — never a correctness input; `--force` bypasses it. |
| Pull pending set | manifest entries − corpus-known `(machine, path, sha)` versions | Pull fetches only unknown blobs and parses each file version once. |
| Corpus snapshots table | unique `manifest_sha256` | Re-pulling a known snapshot is a no-op. |
| Corpus file/blob table | file SHA-256 | Raw file/blob payloads are content-addressed and reused across snapshots. |

If two machines observe the same file version, the blob key is identical and only one object is stored. Snapshot identity is the manifest's own SHA-256 — there is no `bundle_id` to name, police, or collide.

### How efficiency is preserved

- **Content-addressed writes:** a blob key *is* the SHA-256 of its contents; the local driver and R2 both treat an existing key as already-done (`ExistingOK`), and the pointer/index use conditional writes.
- **Per-machine namespaces:** a machine writes only its own manifests and pointer; there is no shared catalogue to merge or repair.
- **Delta push:** the parent manifest is the diff baseline, so push is O(day's delta), not O(history). The advisory capture cache makes the scan itself O(changed files).
- **Delta pull:** anti-entropy — fetch only blobs the corpus doesn't know, parse each file version once.
- **No LIST on steady-state paths:** push/pull/refresh/status learn remote state from pointer + manifest GETs; only `depot verify` may LIST.
- **Local query engine:** search/read never scan depot objects and never query R2; all analysis uses SQLite + FTS5 locally.
- **Deep verification is explicit:** fetching/rehashing every referenced blob is `depot verify --deep`, not part of any daily path.
- **No GC:** the depot never deletes. At R2's storage price and O(unique-bytes) growth, keeping every file version ever is rational and removes the hardest operational problem.

## Trust boundaries

- Source histories are read-only. JSONL adapters never write source roots; OpenCode copies its SQLite DB/WAL/SHM into a private export cache before parsing generated JSONL.
- Default depot and corpus are local.
- R2 is explicit opt-in through `--depot r2:BUCKET` or config; CLI depot overrides require an explicit `r2:` or `local:` kind.
- Network imports are confined to `internal/depot` (outbound R2/S3), `internal/server` (the inbound loopback dashboard), and the `internal/cli/command_serve.go` wrapper that constructs it; a static test enforces this allowlist. Search, read, and ingest remain network-free.
- The dashboard (`aha serve`) binds to loopback by default, validates the `Host` header against a loopback allowlist on every request, requires `application/json` on POST routes, and is read-only; non-loopback binds require explicit `--allow-remote`.
- R2 credentials are not stored in blobs, manifests, config output, command JSON, or logs.
- default `none-v1` does not redact; `redaction:"v1"` redacts corpus projections, while depot blobs remain raw/private.

## Package map

| Package | Responsibility |
|---|---|
| `cmd/aha` | Thin executable entry point. |
| `cmd/aha-gen-ts` | Regenerates the TypeScript client surface from the Go result types. |
| `internal/cli` | Command parsing, JSON errors, renderers, registry/docs generation, command orchestration. |
| `internal/config` | JSONC defaults/load/write. |
| `internal/adapters` | Source discovery/parsing for Pi, Claude Code, Codex, and OpenCode. |
| `internal/archive` | Snapshot capture (discover/stat/hash), parent-manifest diff, advisory capture cache, deterministic manifest encoding; the v1 `tar.zst` reader/writer survives only behind `export`/`import`. |
| `internal/opencodeexport` | Private OpenCode SQLite-to-JSONL export cache: serialized DB/WAL/SHM copy, deterministic JSONL writes, stale-export pruning. |
| `internal/depot` | Local/R2 depot v2 drivers: write-once blob/manifest puts, conditional pointer/index updates, machine-scoped write namespaces, verify. |
| `internal/corpus` | SQLite schema, ingest transaction, read/status/conflict APIs. |
| `internal/search` | FTS5 query construction and result mapping. |
| `internal/mcp` | Read-only MCP server. Wire format, lifecycle, tool registration, schema derivation, strict `additionalProperties:false` input validation, and result envelopes are owned by [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) v1.6+. This package holds the typed input structs, the pure `do<Tool>` business functions, the `CallTool` dispatch the HTTP server reuses (with a `decodeInput` strict-decode that mirrors the SDK's unknown-key rejection on the non-SDK path), and `codegen/` for the TypeScript surface. |
| `internal/server` | Read-only HTTP dashboard: routes the MCP tool surface plus a `go:embed` UI, with loopback/Host/Content-Type/CSP hardening. |
| `internal/model` | Shared config, manifest, parsed-session, result/ref types. |
| `internal/safety` | Path and source-root safety checks. |
| `internal/media` | Image/artefact classification helpers. |

The CLI, the MCP server, and the HTTP dashboard share one JSON contract for
read-side tool execution: `internal/mcp.CallTool` is the dispatch point the
HTTP dashboard reuses, while CLI commands call the same corpus/search APIs.
Adding a read tool still requires wiring its CLI command, HTTP route, and TS
transport entry; drift tests pin those lists. `cmd/aha-gen-ts` projects the Go
result types into `clients/typescript/aha-mcp.ts` for code-mode agent runtimes.

## Design invariants

- Preserve raw depot blobs; optionally redact derived corpus projections with `redaction:"v1"`.
- Blobs and manifests are immutable, write-once, and content-addressed by SHA-256; the manifest SHA *is* the snapshot identity.
- The depot is append-only: nothing is ever deleted (no GC).
- Steady-state paths never LIST; they navigate pointer → manifest → blobs.
- The corpus is derived and rebuildable from depot snapshots.
- SQLite + FTS5 is the search engine; no custom search index.
- Search snippets are leads; agents should `read` refs before answering.
- Public JSON/refs/docs are tested contracts.
