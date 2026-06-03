# Architecture

`aha` is a local-first agent-history archive. It reads supported coding-agent history roots, snapshots them into deterministic `tar.zst` bundles, stores those bundles in a depot, ingests pending bundles into a local SQLite + FTS5 corpus, and exposes stable search/read refs for humans and agents.

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
      │ JSONC defaults  │             │ Pi / Claude / Codex │
      └───────┬────────┘             └──────────┬──────────┘
              │                                 │ read-only discovery/parse
              │                                 ▼
              │                     ┌─────────────────────────┐
              │                     │ Local agent history roots│
              │                     │ ~/.pi ~/.claude ~/.codex│
              │                     └──────────┬──────────────┘
              │                                │ stable copies
              ▼                                ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                              internal/archive                               │
│ Capture → manifest.json + copied raw files → deterministic tar.zst bundle    │
└──────────────────────────────────────┬───────────────────────────────────────┘
                                       │ PutBundle / Fetch / List / Verify
                                       ▼
┌──────────────────────────────────────────────────────────────────────────────┐
│                               internal/depot                                │
│ local: filesystem depot          r2: S3-compatible Cloudflare R2 depot       │
│ bundles/v1/<sha>.tar.zst         catalog/v1/<machine>.json                  │
└──────────────────────────────────────┬───────────────────────────────────────┘
                                       │ ingest pending bundle refs
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
| Source root | A local history directory for Pi, Claude Code, or Codex. | External input; `aha` treats it read-only. |
| Bundle | Immutable deterministic `tar.zst` snapshot containing `manifest.json` and copied raw files. | Yes. Bundle bytes are durable truth. |
| Depot | Bundle store addressed as `local:PATH` or `r2:BUCKET`. | Yes. Stores content-addressed bundle objects. |
| Catalog shard | Repairable JSON listing of bundle refs for one machine. | No. It is rebuilt from bundle objects. |
| Corpus | Local SQLite + FTS5 database and blob store derived from ingested bundles. | Rebuildable. It is the query engine, not truth. |
| Ref | Stable canonical search/read identifier: `msg:v1:<b64-session>:<b64-entry>`, `session:v1:<b64-session>`, or `artifact:v1:<sha256>`. | API contract for retrieval. |

## On-disk / object layouts

### Depot

Local depot default: `~/.aha/depot`.

```text
<depot>/
  depot.json
  bundles/v1/<bundle_sha256>.tar.zst
  catalog/v1/<safe-machine-id>.json
```

R2 uses the same key layout inside the bucket:

```text
depot.json
bundles/v1/<bundle_sha256>.tar.zst
catalog/v1/<safe-machine-id>.json
```

The catalog filename uses a sanitized machine ID for path safety; the JSON body preserves the real `machine_id`. The catalog is an acceleration/provenance layer. `aha depot verify --repair` can rebuild it by listing `bundles/v1/*`, reading each embedded manifest, and writing fresh shards.

See [`depot-lifecycle.md`](depot-lifecycle.md) for the states a depot moves through and the commands that move it (`init`, `use`, `snapshot`/`refresh`, `verify`, `compact`).

### Corpus

Default corpus: `~/.aha`.

```text
<corpus>/
  corpus.db
  blobs/...
  images/...
```

SQLite tables store machines, sources, bundles, files, sessions, entries, messages, artifacts, images, and conflicts. FTS5 virtual tables index message text and text artifacts. Blob files preserve raw bundle content needed for reads.

## Command flows and walkthrough

The shortest mental model is:

```text
local agent histories → snapshot bundle → depot → local corpus → search/read
```

### `aha snapshot`

```text
config + flags
  → adapters discover local sessions/artifacts read-only
  → archive.Capture copies stable raw bytes into temp storage
  → archive.Write creates deterministic tar.zst
  → depot.PutBundle stores bundles/v1/<sha>.tar.zst
  → catalog shard is merged/updated
```

Snapshot creates durable evidence and writes only to the depot; it does not touch the corpus.

### `aha ingest`

With explicit paths, ingest reads those bundles. With no paths, ingest reads pending refs from the configured depot.

```text
depot.List or explicit paths
  → skip bundle SHA already present in corpus
  → depot.Fetch when needed
  → verify catalog SHA/key == actual bundle SHA
  → archive validation and manifest/file SHA checks
  → corpus planner/blob publisher/writer transaction
  → SQLite rows + FTS + blobs
```

Ingest builds the query corpus. It never rereads mutable source roots to decide identity; identity comes from bundled bytes.

### `aha refresh`

`refresh` is the daily path:

```text
snapshot current local sources into depot, or reuse matching state metadata
then ingest pending/new depot bundles into local corpus
```

If source state is unchanged and no deterministic metadata override was supplied, refresh reuses the equivalent existing depot bundle instead of creating a new one.

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

The corpus verifier checks rebuildable local state. It does not contact the depot; use `depot verify` for depot object/catalog integrity. `depot verify` is quick by default and checks metadata/object existence; use `depot verify --deep` for byte/manifest validation.

### `aha depot verify --repair`

```text
validate depot.json schema/layout
list bundle objects
rehash/read embedded manifests
compare object set with catalog refs
optionally rebuild catalog shards from objects
```

Bundle objects are durable truth; catalog shards are repairable.

## Multiple snapshots, aggregation, deduplication, and efficiency

A user can capture many snapshots from one or many machines and publish them to the same depot. Today publishing happens through `aha snapshot --depot ...` or `aha refresh --depot ...`; explicit path ingestion (`aha ingest bundle.tar.zst`) imports into the corpus and does not publish that existing bundle into the depot.

```text
machine A snapshot A1 ─┐
machine A snapshot A2 ─┼─→ depot bundle pool → local corpus on each machine
machine B snapshot B1 ─┘
```

Aggregation is a union over depot catalog shards:

```text
bundles/v1/<sha-a1>.tar.zst
bundles/v1/<sha-a2>.tar.zst
bundles/v1/<sha-b1>.tar.zst
catalog/v1/machine-a.json  # refs A1, A2
catalog/v1/machine-b.json  # refs B1
```

`aha depot ls`, no-argument `aha ingest`, and `status --depot` read catalog shards and treat their bundle refs as the depot's current known set. Each machine has its own shard so normal publishing does not require every machine to write the same catalog object.

The catalog is not absolute truth. If shards are stale or corrupt, `aha depot verify --repair` scans `bundles/v1/*`, reads each embedded manifest, and rewrites catalog shards from the object set.

### How deduplication works

| Layer | Key | Effect |
|---|---|---|
| Depot object store | `bundle_sha256` in `bundles/v1/<sha>.tar.zst` | Identical bundle bytes are stored once. |
| Depot catalog merge | `bundle_sha256` | Re-adding the same bundle ref updates/keeps one ref instead of appending duplicates. |
| Refresh source-state check | catalog `state_sha256` / manifest state signature ignoring `bundle_id`/`captured_at` | Unchanged sources reuse an equivalent existing same-machine bundle without fetching old bundle bytes when state metadata is present. |
| Ingest pending set | `catalog bundle_sha256 - corpus bundle_sha256` | No-argument ingest fetches/imports only bundles not already in the corpus. |
| Corpus bundle table | unique `bundle_sha256` and `bundle_id` | Re-ingesting the same bundle is a duplicate no-op/audit attempt. |
| Corpus file/blob table | file SHA-256 | Raw file/blob payloads are content-addressed and reused across ingested bundles. |

If two machines somehow produce byte-identical bundles, the depot object key is identical and only one object is needed. If two snapshots contain many of the same raw files but differ as bundles, the corpus still deduplicates individual file blobs by file SHA.

### How efficiency is preserved

- **Content-addressed writes:** local depot checks whether the target object exists; R2 checks object existence and uses conditional writes.
- **Per-machine catalog shards:** publishing one machine's bundle only updates that machine's shard, reducing write contention.
- **Delta ingest:** no-arg `ingest` computes `catalog - corpus` and skips already-ingested bundle SHAs.
- **Idempotent refresh:** unchanged sources avoid creating another bundle unless deterministic metadata overrides are supplied; catalog `state_sha256` makes the common case metadata-only.
- **Local query engine:** search/read never scan depot objects and never query R2; all analysis uses SQLite + FTS5 locally.
- **Repair/deep verification is explicit:** expensive full object listing/rehashing is done by `depot verify --deep` or `depot verify --repair`, not on every search or refresh.

Current note: new catalog refs include `state_sha256` and `manifest_sha256`, so unchanged-source checks compare metadata first and fetch old bundles only as a repair/fallback path for refs missing state metadata.

## Trust boundaries

- Source adapters are read-only.
- Default depot and corpus are local.
- R2 is explicit opt-in through `--depot r2...` or config.
- Network imports are confined to `internal/depot` (outbound R2/S3), `internal/server` (the inbound loopback dashboard), and the `internal/cli/command_serve.go` wrapper that constructs it; a static test enforces this allowlist. Search, read, and ingest remain network-free.
- The dashboard (`aha serve`) binds to loopback by default, validates the `Host` header against a loopback allowlist on every request, requires `application/json` on POST routes, and is read-only; non-loopback binds require explicit `--allow-remote`.
- R2 credentials are not stored in bundles, catalogs, config output, command JSON, or logs.
- v1 does not redact secrets; bundles and corpora are private artifacts.

## Package map

| Package | Responsibility |
|---|---|
| `cmd/aha` | Thin executable entry point. |
| `cmd/aha-gen-ts` | Regenerates the TypeScript client surface from the Go result types. |
| `internal/cli` | Command parsing, JSON errors, renderers, registry/docs generation, command orchestration. |
| `internal/config` | JSONC defaults/load/write. |
| `internal/adapters` | Source discovery/parsing for Pi, Claude Code, and Codex. |
| `internal/archive` | Snapshot capture, deterministic archive writing, bundle validation, state signature. |
| `internal/depot` | Local/R2 depot drivers, catalog merge/list/fetch/verify/repair. |
| `internal/corpus` | SQLite schema, ingest transaction, read/status/conflict APIs. |
| `internal/search` | FTS5 query construction and result mapping. |
| `internal/mcp` | Read-only MCP server. Wire format, lifecycle, tool registration, schema derivation, strict `additionalProperties:false` input validation, and result envelopes are owned by [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk) v1.6+. This package holds the typed input structs, the pure `do<Tool>` business functions, the `CallTool` dispatch the HTTP server reuses (with a `decodeInput` strict-decode that mirrors the SDK's unknown-key rejection on the non-SDK path), and `codegen/` for the TypeScript surface. |
| `internal/server` | Read-only HTTP dashboard: routes the MCP tool surface plus a `go:embed` UI, with loopback/Host/Content-Type/CSP hardening. |
| `internal/model` | Shared config, manifest, parsed-session, result/ref types. |
| `internal/safety` | Path and source-root safety checks. |
| `internal/media` | Image/artifact classification helpers. |

The CLI, the MCP server, and the HTTP dashboard are three skins over one
JSON contract: `internal/mcp.CallTool` is the single dispatch point, so adding
or changing a read tool updates all three surfaces at once. `cmd/aha-gen-ts`
projects the Go result types into `clients/typescript/aha-mcp.ts` for
code-mode agent runtimes.

## Design invariants

- Preserve raw data; do not redact in v1.
- Bundles are immutable and content-addressed by SHA-256.
- The corpus is derived and rebuildable from depot bundles.
- SQLite + FTS5 is the search engine; no custom search index.
- Search snippets are leads; agents should `read` refs before answering.
- Public JSON/refs/docs are tested contracts.
