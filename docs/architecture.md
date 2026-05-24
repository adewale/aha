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
| Ref | Stable search/read identifier such as `<session>#<entry>` or `artifact:<sha>`. | API contract for retrieval. |

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

### Corpus

Default corpus: `~/.aha`.

```text
<corpus>/
  corpus.db
  blobs/...
  images/...
```

SQLite tables store machines, sources, bundles, files, sessions, entries, messages, artifacts, images, and conflicts. FTS5 virtual tables index message text and text artifacts. Blob files preserve raw bundle content needed for reads.

## Main flows

### `aha snapshot`

```text
config + flags
  → adapters discover local sessions/artifacts read-only
  → archive.Capture copies stable raw bytes into temp storage
  → archive.Write creates deterministic tar.zst
  → depot.PutBundle stores bundles/v1/<sha>.tar.zst
  → catalog shard is merged/updated
```

Snapshot does not touch the corpus. It writes only to the depot.

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

Ingest never rereads mutable source roots to decide identity. Identity comes from bundled bytes.

### `aha refresh`

`refresh` is the daily path:

```text
snapshot current local sources into depot
then ingest pending depot bundles into local corpus
```

If source state is unchanged and no deterministic metadata override was supplied, refresh reuses the equivalent existing depot bundle instead of creating a new one.

### `aha search` and `aha read`

```text
search query → SQLite FTS5 → results with structured ref/ref_text
read <ref>   → corpus read APIs → transcript window or artifact body
```

Search and read never contact the depot. They are local/offline corpus operations.

### `aha depot verify --repair`

```text
validate depot.json schema/layout
list bundle objects
rehash/read embedded manifests
compare object set with catalog refs
optionally rebuild catalog shards from objects
```

Bundle objects are durable truth; catalog shards are repairable.

## Trust boundaries

- Source adapters are read-only.
- Default depot and corpus are local.
- R2 is explicit opt-in through `--depot r2...` or config.
- Only `internal/depot` may import network packages.
- R2 credentials are not stored in bundles, catalogs, config output, command JSON, or logs.
- v1 does not redact secrets; bundles and corpora are private artifacts.

## Package map

| Package | Responsibility |
|---|---|
| `cmd/aha` | Thin executable entry point. |
| `internal/cli` | Command parsing, JSON errors, renderers, registry/docs generation, command orchestration. |
| `internal/config` | JSONC defaults/load/write. |
| `internal/adapters` | Source discovery/parsing for Pi, Claude Code, and Codex. |
| `internal/archive` | Snapshot capture, deterministic archive writing, bundle validation, state signature. |
| `internal/depot` | Local/R2 depot drivers, catalog merge/list/fetch/verify/repair. |
| `internal/corpus` | SQLite schema, ingest transaction, read/status/conflict APIs. |
| `internal/search` | FTS5 query construction and result mapping. |
| `internal/model` | Shared config, manifest, parsed-session, result/ref types. |
| `internal/safety` | Path and source-root safety checks. |
| `internal/media` | Image/artifact classification helpers. |

## Design invariants

- Preserve raw data; do not redact in v1.
- Bundles are immutable and content-addressed by SHA-256.
- The corpus is derived and rebuildable from depot bundles.
- SQLite + FTS5 is the search engine; no custom search index.
- Search snippets are leads; agents should `read` refs before answering.
- Public JSON/refs/docs are tested contracts.
