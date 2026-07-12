---
title: Agent History Aggregator Spec
kind: spec
created: 2026-05-18
updated: 2026-05-19
tags:
  - agents
  - session-history
  - searchable-corpus
  - snapshots
  - multi-machine
  - pi
  - claude-code
  - go
  - cli
  - specification
source_type: design
source_count: 8
status: draft-spec
aliases:
  - Agent History Aggregator
  - aha
  - Agent session snapshot corpus
---

# Agent History Aggregator Spec

*Historical: this is the original v1 spec and predates depot v2 ([docs/depot-v2-spec.md](depot-v2-spec.md)). The bundle/catalogue snapshot mechanics described here were replaced by content-addressed snapshots (write-once blobs + per-machine manifests) in June 2026; the v1 `tar.zst` bundle format survives only behind `aha archive status` / `aha archive download <bundle.tar.zst>`. The product boundary, adapters, corpus, and trust posture remain.*

## Product boundary

Agent History Aggregator, `aha` for short, is an open-source Go CLI that archives, merges, and searches coding-agent session histories across machines.

The v1 sources are:

- Pi session files;
- Claude Code session files.

The workflow:

```txt
machine-local agent sessions
→ dated compressed tar.zst bundle with machine_id
→ ingest bundle into a local corpus
→ dedupe/merge bundles from the same and different machines
→ search aggregate history
```

Short version:

> Snapshot the sessions. Merge the bundles. Search the aggregate.

## Locked decisions

| Area | Decision |
|---|---|
| Tool name | Agent History Aggregator (`aha`) |
| Product form | Open-source CLI |
| Language | Go |
| Source adapters in v1 | Pi and Claude Code built in; adapter system must be pluggable for more agents later |
| Compression/archive format | `tar.zst` |
| Paths | Preserve raw paths by default |
| SQLite | Use SQLite as the corpus/query engine; use FTS5, indexes, constraints, transactions, JSON columns/functions where useful; do not reimplement what SQLite does well |
| Config format | JSONC |
| Secrets/redaction | `none-v1` default preserves raw projections; `redaction:"v1"` redacts corpus projections; bundles remain raw |
| Subagents | Include in v1 |
| Images | Include in v1 and store enough information to recreate image-bearing prompts |
| Live sync | No |
| Live session mutation | Never |
| Embeddings | Not v1 |
| Tool output | Preserve in raw files; ignore for v1 search/indexing |
| Determinism | Required for manifests, archive layout, tar metadata, and stable test fixtures |
| Windows support | Punt to v2 |

## Non-goals

V1 does not try to:

- mutate live Pi or Claude Code session files;
- continuously sync sessions between machines;
- resume a session from another machine;
- publish or redact a public dataset;
- solve secret handling;
- inject old session history into every new agent run;
- build a hosted service;
- support Windows in v1.

The tool is a local archive and search system. Users should treat bundles as private because they remain raw; corpora are private unless ingested with `redaction:"v1"` and reviewed.

## What is a `.zst` file?

`.zst` means the file is compressed with Zstandard.

For this tool, the bundle is a tar archive compressed with Zstandard:

```txt
many files + manifest
→ tar archive
→ zstd compression
→ bundle.tar.zst
```

Implementation target:

- use Go's `archive/tar` for the tar container;
- use `github.com/klauspost/compress/zstd` or equivalent pure-Go Zstandard implementation for compression;
- do not require users to have a separate `zstd` binary installed.

## Source adapters

The CLI should keep source-specific discovery and parsing behind adapters. Pi and Claude Code are the only built-in v1 adapters, but the architecture must not bake either source into archive, ingest, search, or read semantics. Future agents should be addable by implementing the adapter contract and registering the adapter.

```go
type SourceAdapter interface {
    Name() string
    Version() string
    DefaultRoots() []DefaultRoot
    Capabilities() AdapterCapabilities
    Discover(ctx context.Context, config SourceConfig) ([]SessionFile, error)
    DiscoverArtifacts(ctx context.Context, session SessionFile) ([]ArtifactFile, error)
    ParseSession(ctx context.Context, file SessionFile, r io.Reader) (*ParsedSession, error)
}
```

Adapter rules:

- adapters emit source-native IDs when available and stable derived IDs when not;
- adapters preserve raw files and raw entries even when normalization is partial;
- adapters can attach source-specific metadata as JSON, but core tables must remain source-agnostic;
- adapters describe capabilities such as `HasThreads`, `HasSubagents`, `HasImages`, `HasToolCalls`, `HasStableEntryIDs`, and `CanLinkSubagents`;
- adapter name and adapter version are recorded in manifests and SQLite so parsed data can be regenerated after parser changes;
- v1 adapters may be compiled in; external plugin loading can be v2, but the package boundary should make that migration straightforward.

Adapters in v1:

| Adapter | Scope |
|---|---|
| `pi` | Pi JSONL sessions and related subagent artefacts. |
| `claude-code` | Claude Code sessions and related subagent/image artefacts. |

Preserve native source files in the bundle. Normalize into database tables during ingest.

## Reality capture: Pi

Pi stores sessions as JSONL files.

Relevant local Pi docs:

- `/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent/docs/session-format.md`
- `/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent/README.md`
- `/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent/docs/settings.md`
- `/opt/homebrew/lib/node_modules/@earendil-works/pi-coding-agent/docs/extensions.md`

Key facts:

- default session directory: `~/.pi/agent/sessions/`;
- session files are JSONL;
- paths look like `~/.pi/agent/sessions/--<cwd-with-slashes-replaced>--/<timestamp>_<uuid>.jsonl`;
- the first JSONL line is a session header with `type`, `version`, `id`, `timestamp`, and `cwd`;
- entries form a tree with `id` and `parentId`;
- Pi session format is currently version 3;
- roles include `user`, `assistant`, `toolResult`, `bashExecution`, `custom`, `branchSummary`, and `compactionSummary`;
- the full history remains in the JSONL file after compaction;
- the local session tree can contain sidecar directories such as `subagent-artifacts/`.

Local sample at inspection time:

```txt
session directory: /Users/adewale/.pi/agent/sessions
JSONL session files: 43
session directory size: 159M
```

## Reality capture: Claude Code

V1 supports Claude Code, not Claude Desktop exports, Anthropic Workbench logs, or generic Anthropic API logs.

Reference implementation inspected:

- `adewale/claude-history-explorer`, a Python CLI for local Claude Code JSONL history;
- `adewale/claude-history-explorer#8`, an open Windows compatibility PR.

Confirmed or strongly evidenced facts:

- default session directory: `~/.claude/projects/`;
- project directories encode source project paths;
- Unix/macOS encoded path example: `/Users/foo/myproject` becomes `-Users-foo-myproject`;
- Windows encoded path example from PR #8: `C:\Users\Moho\project` becomes `C--Users-Moho-project`;
- session files are `*.jsonl` under each project directory;
- files prefixed with `agent-` are Claude Code subagent conversations;
- each JSONL line is a single JSON object;
- common top-level fields include `type`, `timestamp`, and sometimes `slug`;
- top-level `type` commonly includes `user` and `assistant`;
- message payloads live under `message`;
- `message.content` can be a string or a list of content blocks;
- observed block types include `text`, `tool_use`, and `tool_result`;
- assistant messages can include `message.usage` token accounting and `message.model`;
- transcript content can contain Unicode such as emoji and em dashes, so CLI output and tests must be UTF-8-safe.

Discovery rules for v1:

- default source root is `~/.claude/projects/` unless overridden;
- project candidates are non-hidden directories under the source root;
- do not require project directory names to start with `-`; this is cheap and avoids a known future Windows portability trap, but it is not a v1 Windows support promise;
- include a directory only if it contains one or more `*.jsonl` files;
- preserve both raw project directory name and raw filesystem path in the manifest;
- treat path decoding as best-effort metadata, not as identity, because Claude Code path encoding is lossy and platform-dependent.

Parser rules for v1:

- stream JSONL line-by-line;
- skip malformed lines only after recording parse diagnostics; do not fail an entire bundle for one bad line;
- preserve every raw JSONL line/object in the file blob even if normalization fails;
- normalize only fields that are present and understood;
- index `user` and `assistant` text blocks by default;
- record `tool_use` names and inputs as metadata when cheap;
- preserve `tool_result` in raw files, but do not index tool-result output for v1 search;
- use source-native IDs if present; otherwise derive stable entry IDs from file path plus line number plus entry hash.

Still to verify against real Claude Code data during implementation:

- exact default directory on Linux under current Claude Code versions;
- Windows default directory and drive-letter handling for v2;
- whether stable message UUID fields are present in all entries;
- how images and attachments are represented;
- whether active files are append-only and safe to copy with retry;
- whether subagent sessions can be linked to parent sessions beyond the `agent-` filename convention.

## Bundle format

Recommended file name:

```txt
aha-sessions-{machine_id}-{captured_at_utc}-{bundle_id}.tar.zst
```

Example:

```txt
aha-sessions-ade-mbp-2026-05-18T20-30-00Z-4b9b7a.tar.zst
```

Archive layout:

```txt
manifest.json
sources/
  pi/
    sessions/
      --Users-adewale-Documents-projects-code-xampler--/
        2026-05-09T15-07-41-968Z_019e0d47-ae0f-7538-b05a-19a6bd3dbf61.jsonl
    artifacts/
      subagent-artifacts/
        ...
  claude-code/
    sessions/
      ...
    artifacts/
      ...
checksums/
  sha256sums.txt
```

Archive determinism is a v1 requirement. Given the same input files, machine/config values, and pinned capture metadata, snapshot must produce byte-identical `manifest.json`, tar entry ordering, tar metadata, and compressed bundle bytes.

Determinism rules:

- stable lexical ordering for discovered files and manifest arrays;
- canonical JSON for `manifest.json` with stable field ordering and no incidental whitespace;
- normalized tar metadata: owner/group IDs, owner/group names, modes, mtimes, type flags, and path separators;
- deterministic zstd settings with no embedded wall-clock metadata;
- bundle SHA-256 computed after archive creation and emitted in command output / JSON;
- tests pin `captured_at`, `bundle_id`, and machine/config values to prove deterministic fixtures.

## Manifest

`manifest.json` identifies the bundle and lets the ingester verify contents without trusting the filename.

```json
{
  "schema": "agent-session-snapshot-bundle/v2",
  "bundle_id": "uuid-or-ulid",
  "machine_id": "ade-mbp",
  "machine_label": "Adewale MacBook Pro",
  "captured_at": "2026-05-18T20:30:00Z",
  "created_by": "aha 0.1.0",
  "implementation": {
    "language": "go",
    "archive": "tar.zst"
  },
  "source": {
    "host_os": "darwin",
    "hostname_hash": "sha256:...",
    "user_hash": "sha256:..."
  },
  "policy": {
    "path_mode": "raw",
    "include_subagents": true,
    "include_images": true,
    "index_tool_output": false,
    "redaction": "none-v1"
  },
  "counts": {
    "session_files": 43,
    "artifact_files": 12,
    "image_files": 4,
    "bytes_uncompressed": 166000000
  },
  "files": [
    {
      "source": "pi",
      "kind": "session",
      "relative_path": "sources/pi/sessions/--Users-adewale-Documents-projects-code-xampler--/2026-05-09T15-07-41-968Z_019e0d47-ae0f-7538-b05a-19a6bd3dbf61.jsonl",
      "raw_path": "/Users/adewale/.pi/agent/sessions/--Users-adewale-Documents-projects-code-xampler--/2026-05-09T15-07-41-968Z_019e0d47-ae0f-7538-b05a-19a6bd3dbf61.jsonl",
      "sha256": "...",
      "bytes": 123456,
      "session_id": "019e0d47-ae0f-7538-b05a-19a6bd3dbf61",
      "cwd": "/Users/adewale/Documents/projects/code/xampler",
      "started_at": "2026-05-09T15:07:41.968Z",
      "entries": 123
    }
  ]
}
```

## Machine identity

Machine identity is explicit and user-controlled.

Examples:

```txt
ade-mbp
work-mac
linux-box
cloud-devbox-01
```

Default `machine_id` is a sanitised local hostname, written visibly by `aha init`. Users can override it in config or with a flag when they need stable naming across host renames:

```bash
aha archive upload --machine ade-mbp
```

Raw paths are preserved by default because they are useful for search and project reconstruction. Hostname and username should be hashed by default if stored in the manifest.

## Snapshot command

Example:

```bash
aha archive upload \
  --machine ade-mbp \
  --source pi=$HOME/.pi/agent/sessions \
  --source claude-code=$HOME/.claude/projects \
  --archive local:~/.aha/depot
```

Responsibilities:

1. Read config and CLI flags.
2. Resolve source directories.
3. Discover sessions, subagent artefacts, and images through source adapters.
4. Copy live files safely without modifying them.
5. Compute SHA-256 for every file.
6. Parse enough metadata to build the manifest.
7. Write deterministic `manifest.json`.
8. Write `tar.zst` bundle.
9. Compute bundle SHA-256.
10. Emit bundle path/key and SHA in command output / JSON.

By default (`none-v1`) aha does not redact secrets. The command should print a direct warning unless a non-interactive flag accepts it:

```txt
`none-v1` does not redact secrets, and bundles always remain raw. Bundles may contain prompts, source code, tool output, images, tokens, and private paths. Treat the bundle as private.
```

## Active file handling

Session files may be appended while an agent is running.

Recommended v1 policy:

1. Copy candidate file to a temp location.
2. Re-stat and hash source file.
3. If size or mtime changed during copy, retry once.
4. If still changing, include the copied version but mark it as unstable in the manifest.

Manifest field:

```json
{
  "copy_state": "stable|unstable"
}
```

## Subagents and artefacts

V1 includes subagent artefacts.

Artefact handling rules:

- include adapter-recognised subagent sidecar files;
- preserve raw artefact files;
- compute content hashes;
- link artefacts to parent sessions when inferable;
- keep unlinked artefacts with `parent_session_id = null`;
- index text artefacts as documents;
- do not throw away artefacts just because linkage is uncertain.

Pi examples observed locally:

```txt
subagent-artifacts/*_input.md
subagent-artifacts/*_output.md
subagent-artifacts/*_meta.json
```

Claude Code examples inferred from `claude-history-explorer`:

```txt
~/.claude/projects/<encoded-project>/<session-id>.jsonl
~/.claude/projects/<encoded-project>/agent-<id>.jsonl
```

For v1, treat `agent-*.jsonl` as subagent session files. Parent linkage is best-effort; if no explicit parent/session reference is found, preserve them as sessions with `is_subagent = true` and `parent_session_id = null`.

## Images

V1 includes images.

Image handling rules:

- preserve image files, embedded base64 image payloads, and source-native image references;
- content-address image blobs by SHA-256;
- store MIME type, extension, byte size, dimensions when cheaply available, source, raw path or source reference, parent session, parent entry ID, content block index, and prompt order;
- keep enough linkage metadata to fully recreate the original image-bearing prompt as presented to the agent, including text/image ordering and the original raw entry JSON;
- avoid dumping base64 image bodies into FTS snippets;
- extract embedded base64 images into image blobs during ingest while preserving the raw entry JSON;
- dedupe identical image bytes across bundles while preserving every prompt/reference occurrence.

V1 does not require OCR or image captioning. Image text search is metadata-only unless the original prompt contains text around the image.

## Ingest command

Example:

```bash
aha archive download --archive local:~/.aha/depot
```

Responsibilities:

1. Verify archive can be decompressed.
2. Read and validate `manifest.json`.
3. Check bundle duplicate.
4. Store bundle blob by SHA-256.
5. For each session file:
   - verify file SHA;
   - parse through the relevant source adapter;
   - store file blob if new;
   - register session version;
   - parse entries;
   - upsert entry rows;
   - quarantine conflicting entry IDs.
6. Store artefacts and images by content hash.
7. Update FTS index.
8. Write ingest report.

## Dedupe and merge model

Dedupe happens at four levels:

| Level | Key | Purpose |
|---|---|---|
| Bundle | `bundle_id` and bundle SHA-256 | Avoid ingesting the same archive twice. |
| File | file SHA-256 | Avoid storing identical files repeatedly. |
| Session | `source` + source-native session ID + machine provenance | Track session versions across snapshots. |
| Entry | source-native session ID + entry ID + entry SHA-256 | Merge append-only growth safely. |

A later bundle from the same machine may contain a longer version of a session already seen. The ingester should parse entries and add only new entry IDs while recording the new file version.

Merge cases:

| Case | Behaviour |
|---|---|
| Same bundle seen again | Skip; record duplicate ingest attempt. |
| Same file hash seen again | Reuse stored blob; link it to the new bundle. |
| Same session ID, later file has extra entries | Add new entries; update latest file-version pointer. |
| Same session ID, same entry ID, same entry hash | No-op. |
| Same session ID, same entry ID, different entry hash | Conflict; quarantine for review. |
| Different machines have identical file | Deduplicate blob, keep both provenance records. |
| Different machines have same session ID with divergent entries | Conflict unless adapter can prove a copy/fork relationship. |

Core invariant:

> Never delete a session entry during ingest. Add, link, or quarantine.

## Corpus store

Use SQLite plus filesystem blobs. SQLite is the corpus engine, not a cache. The design should lean on SQLite for joins, constraints, uniqueness, transactions, indexes, JSON metadata, FTS5 search, and conflict queries instead of reimplementing mini-databases or custom search/merge engines in Go. Filesystem blobs are for large immutable bytes; SQLite owns metadata, provenance, and queryability.

Directory layout:

```txt
~/.aha/
  corpus.db
  blobs/
    bundles/{bundle_sha256}.tar.zst
    files/{file_sha256}.zst        # includes artifact file bytes
    images/{image_sha256}.{ext}
```

Artefact bytes are stored as ordinary file blobs under `blobs/files/`; artefact
text and provenance live in the SQLite `artifacts` table. Ingest reports are
returned to the caller and emitted to stdout/JSON, not persisted to disk.

V1 uses `modernc.org/sqlite` with SQLite FTS5. SQLite schema, FTS, constraints, indexes, transactions, and migrations are part of the product contract; filesystem blobs store immutable large bytes.

Possible tables:

```sql
bundles(bundle_id, bundle_sha256, machine_id, captured_at, ingested_at, manifest_json)
machines(machine_id, first_seen_at, last_seen_at, labels_json)
sources(source_id, source_name, adapter_version, capabilities_json)
files(file_sha256, kind, bytes, compressed_blob_path, first_seen_bundle_id)
sessions(session_key, source_name, source_session_id, machine_id, raw_cwd, project_key, started_at, source_metadata_json, is_subagent, parent_session_key)
session_versions(session_key, file_sha256, bundle_id, relative_path, raw_path, observed_at, copy_state)
entries(session_key, entry_id, parent_id, entry_type, timestamp, role, entry_sha256, raw_json, source_metadata_json)
messages(session_key, entry_id, role, text, tool_name, command, files_json, model, provider, tokens, cost)
artifacts(artifact_id, artifact_sha256, source_name, machine_id, bundle_id, kind, parent_session_key, parent_entry_id, raw_path, relative_path, text_preview, text_body)
images(image_sha256, source_name, mime_type, bytes, width, height, ext, blob_path)
entry_assets(session_key, entry_id, asset_sha256, asset_kind, content_index, prompt_order, raw_ref, mime_type, metadata_json)
session_path_tokens(session_key, token)
artifact_path_tokens(artifact_id, token)
conflicts(conflict_id, session_key, entry_id, first_entry_sha256, second_entry_sha256, details_json)
```

Search/path derived tables:

```sql
fts_messages(rowid = messages.rowid, session_key, entry_id, text)
fts_artifacts(rowid = artifacts.artifact_id, artifact_id, text)
session_path_tokens(session_key, token)         -- indexed exact segment tokens
artifact_path_tokens(artifact_id, token)       -- indexed exact segment tokens
```

For v1, store raw source entries and normalized fields. Derived columns can be regenerated. FTS rows are trigger-maintained and repairable with `aha workspace verify --repair-fts`; verifier queries use rowid identity to avoid unindexed FTS key scans. `entry_assets` links images and other prompt assets back to the exact entry and content position needed to reconstruct prompts. `project_key` is an exact indexed project filter; `--path-token` uses the token tables; `--path` remains a convenience contains filter.

## Search command

Examples:

```bash
aha search "loadable ephemeral context"
aha search "dynamic workflows" --machine ade-mbp --after 2026-05-01
aha search "xampler workflows" --source pi --project aha
aha search "workflow" --path-token workflows.py
aha show msg:v1:... --before 3 --after 5
aha status
aha workspace verify
aha workspace conflicts
```

Minimum search features:

| Feature | Query example |
|---|---|
| Full-text message search | `dynamic workflows durable streams` |
| Source filter | `--source pi` or `--source claude-code` |
| Machine filter | `--machine ade-mbp` |
| Date filter | `--after 2026-05-01 --before 2026-05-18` |
| Role filter | `--role user` |
| Project filter | `--project aha` exact indexed `sessions.project_key` |
| Indexed path-token filter | `--path-token workflows.py` exact indexed path segment token |
| Convenience path contains filter | `--path xampler/workflows.py` non-indexed contains match |
| Limit guardrail | `--limit N`, capped at 200 with a CLI warning |
| JSON output | `--json` |
| Session read | bounded context around matching entries |

Default search indexes user and assistant text, summaries, text artefacts, and selected source metadata. V1 preserves raw tool output inside the stored source files but does not index tool output for search.

Default result fields:

```txt
score timestamp source machine project role snippet ref_text session_key entry_id
```

## Ranking

V1 ranking:

- SQLite FTS5 BM25;
- ordinary SQLite indexes for source, machine, role, exact `project_key`, path tokens, and date filters;
- SQL joins for provenance and read-context lookups;
- no semantic embeddings;
- no custom search engine;
- no tool-output ranking because tool output is not indexed in v1.

Do not let embeddings or custom indexing hide bad ingestion. The v1 order is:

```txt
parse correctly
→ dedupe safely
→ FTS search works
→ add metadata filters
→ consider embeddings later
```

## Privacy and v2 candidates

V1 punts secret handling.

The CLI and README must say plainly:

```txt
Bundles remain raw and may contain credentials, private prompts, source code, tool output, images, filesystem paths, and API responses. `redaction:"v1"` only redacts corpus projections. Do not upload bundles or corpora publicly unless you have reviewed them yourself.
```

V2 can add redaction, Windows support, and external plugin loading:

- Windows support;
- external adapter/plugin loading;
- configured secret files;
- deny patterns;
- common token regexes;
- TruffleHog-style scanning;
- redacted derivative bundles;
- public dataset export.

## Config

Use JSONC for configuration so users can keep comments and trailing commas while retaining JSON-compatible structure.

Default config path should use platform-native config directories. On macOS/Linux v1, the likely paths are:

```txt
~/.config/aha/config.jsonc
~/.aha/config.jsonc  # acceptable fallback if platform config discovery is too much for v1
```

Config should cover:

```jsonc
{
  // Required unless supplied on the CLI.
  "machine_id": "ade-mbp",
  "machine_label": "Adewale MacBook Pro",

  "sources": [
    { "type": "claude-code", "root": "~/.claude/projects", "enabled": true },
    { "type": "codex", "root": "~/.codex/sessions", "enabled": true },
    { "type": "pi", "root": "~/.pi/agent/sessions", "enabled": true }
  ],

  "workspace_dir": "~/.aha",
  "archive": { "type": "local", "location": "~/.aha/depot" },
  "path_mode": "raw",
  "include_subagents": true,
  "include_images": true,
  "index_tool_output": false,
  "redaction": "none-v1",
  "acknowledged_raw_history": false
}
```

CLI flags override config values. The first-run UX creates a starter JSONC config with a visible hostname-derived `machine_id`; users can edit it before snapshotting. Recommended journeys and no-flag defaults are documented in `docs/user-journeys.md`.

## CLI shape

```txt
aha archive upload && aha archive download
aha archive upload
aha archive download
aha search
aha show
aha status
aha workspace verify
aha workspace conflicts
aha workspace <status|verify|repair|conflicts>
aha archive <init|set-default|status|upload|download|verify>
aha status
```

Implementation preferences:

| Concern | Preference |
|---|---|
| CLI framework | Standard library `flag` with a tested command registry |
| Compression | pure-Go Zstandard |
| Archive | Go stdlib `archive/tar` |
| SQLite | SQLite + FTS5 as the query engine; prefer pure-Go driver if FTS5/JSON support is reliable |
| Config | JSONC in platform-native config dir |
| Output | human table by default, `--json` for scripts |
| Testing | Go stdlib `testing`, real temp dirs/SQLite/tar.zst/fake-S3, `testdata/` fixtures, property/fuzz tests, query-plan tests, golden-file tests, and pathological benchmarks |

## Thin slice

The first useful implementation should avoid embeddings, sync, redaction, and GUI work.

### Input

- one Pi session directory;
- one Claude Code session directory;
- one machine ID, e.g. `ade-mbp`.

### Processing

- create one `.tar.zst` bundle with `manifest.json`;
- include sessions, subagent artefacts, and images;
- ingest into local SQLite;
- build FTS over user/assistant text, summaries, text artefacts, and selected metadata;
- search one known phrase;
- read bounded context around a hit;
- ingest the same bundle again and prove no duplicate rows.

### Output

```txt
bundle file
corpus.db
ingest report
search result with source, session id, machine id, cwd, timestamp, snippet
read output with surrounding entries
```

## Definition of done for v1

- Go CLI builds on macOS and Linux without CGO.
- `snapshot` creates `tar.zst` bundles without modifying source sessions.
- Bundle manifest records machine ID, capture date, sources, files, hashes, counts, raw paths, subagents, and images.
- `ingest` can ingest the same bundle twice without duplicating logical rows.
- `ingest` can ingest a later bundle from the same machine and add only new entries.
- `search` returns matches across Pi and Claude Code sessions from multiple machines.
- `read` shows bounded context around a result.
- Conflicting same-entry IDs with different hashes are quarantined, not overwritten.
- `status` reports machines, bundles, sources, sessions, entries, artefacts, images, path-token/FTS counts, index size, conflicts, and metadata-only depot-behind counts when `--archive` is explicit.
- `corpus size|vacuum|prune-orphans` exposes explicit local maintenance; `prune-orphans` is dry-run unless `--force`.
- `depot verify` is quick by default, `--deep`/`--repair` are explicit byte-reading integrity operations, and `depot compact` deduplicates catalogue metadata without touching bundle bytes.
- README states that `none-v1` does not redact, `redaction:"v1"` redacts corpus projections, and bundles remain raw.
- Test suite includes realistic Pi and Claude Code fixtures, including `agent-*.jsonl` and image-bearing prompts.
- Test suite proves snapshot read-only behaviour, ingest idempotence, deterministic manifests and compressed bundles, conflict quarantine, parser robustness/fuzz safety, prompt image reconstruction including dimensions when available, search/read coherence, FTS verifier query shape, indexed search filters, depot operation budgets, and maintenance dry-run safety.
- CI/local verification runs `go test ./...`, `go test -race ./...`, `go vet ./...`, bounded fuzz/property tests, deterministic archive tests, documentation-code sync checks, and build verification through `scripts/verify.sh full`.

## Testing strategy

The implementation should borrow directly from `adewale/testing-best-practices`: test quality is measured by bugs caught, not test count or line coverage. Prefer real filesystem/database objects over mocks, write regression tests before fixes, and keep fixtures realistic.

### Test layout

Recommended Go layout:

```txt
cmd/aha/...
internal/adapters/pi/...
internal/adapters/claudecode/...
internal/archive/...
internal/corpus/...
internal/search/...
testdata/
  pi/
  claude-code/
  bundles/
  golden/
```

Use Go's standard testing stack first:

- table-driven `*_test.go` tests with named cases;
- `t.TempDir()` for filesystem snapshots, bundles, and corpora;
- real SQLite databases in temporary directories, not mocked repositories;
- `io.Reader`/`io.Writer` injection so CLI output can be tested without global stdout;
- `go test ./...`, `go test -race ./...`, `go vet ./...` in CI;
- build tags for optional slow or platform-specific tests.

### Required test types

| Test type | Required coverage for `aha` |
|---|---|
| Smoke | `aha --help`, `aha archive upload --help`, `aha archive download --help`, `aha search --help`, and a tiny snapshot→ingest→search flow all run. |
| Unit | Path discovery, JSONL parsing, manifest validation, hash calculation, dedupe keys, FTS query construction. |
| Golden files | Deterministic `manifest.json`, ingest reports, search output, `read` output, conflict reports. |
| Property/fuzz | Parsers and normalizers never panic on arbitrary input; archive write/read roundtrips; ingest is idempotent; deterministic snapshot output is stable for stable inputs. |
| Characterization | Realistic anonymized Pi and Claude Code JSONL fixtures capture current source behaviour before parser refactors. |
| Regression | Every bug gets a named test first; record the Windows Claude project discovery issue from `claude-history-explorer#8` as a v2 fixture, not a v1 support promise. |
| Documentation sync | README command list, flags, config keys, and privacy warning stay in sync with the actual CLI registry/config structs. |
| Race/concurrency | Active-file copy retry behaviour and parallel ingest/search safety pass `go test -race`. |
| Performance | Pathological package benchmarks plus cheap invariants/query-plan tests catch obvious O(n²), extra network fetch/downloads, unindexed FTS/path plans, duplicate catalogue work, and repeated blob recompression. |

### Core properties to test

- **Read-only snapshot:** snapshot never modifies source session files; source mtimes and hashes remain unchanged in stable tests.
- **Archive roundtrip:** decompressing a bundle yields the same manifest and file hashes that snapshot reported.
- **Manifest honesty:** every manifest file entry exists in the archive and every archived session/artefact appears in the manifest.
- **Determinism:** with pinned capture time, bundle ID, file mtimes, and input order, manifest bytes, tar entry ordering, tar metadata, and compressed bundle bytes are stable.
- **Idempotent ingest:** ingesting the same bundle twice does not duplicate logical bundles, files, sessions, entries, messages, artefacts, or images.
- **Append-only merge:** ingesting a later session version adds new entries and never deletes old entries.
- **Conflict safety:** same session/entry ID with different entry hash creates a conflict row and never overwrites the existing entry.
- **Parser robustness:** malformed JSONL lines, unknown roles, unknown block types, invalid timestamps, huge tool outputs, and Unicode text do not crash parsing.
- **Claude discovery:** Unix-style `-Users-...` project directories are discovered if they contain JSONL files; hidden dirs are ignored. Windows-style `C--Users-...` stays in v2 fixtures.
- **Prompt reconstruction:** image-bearing prompts can be recreated from raw entry JSON plus `entry_assets` and image blobs.
- **Search/read coherence:** every search hit can be passed to `aha show` and returns bounded context containing the hit entry.

### Test quality rules

- Avoid mocks for filesystem, tar/zstd, and SQLite behaviour; use real temp dirs, real archives, and real databases.
- Do not accept tests that only assert “not empty” for manifests, search results, or reports; assert specific fields and negative cases.
- No unconditional skipped tests without a tracking issue or build tag rationale.
- No `t.Log`/`t.Logf` in assertion position; use `t.Error`, `t.Errorf`, or `t.Fatal`.
- Golden-file updates require human review because they define compatibility.

## Implementation lessons incorporated

A first thin implementation validated the core direction but exposed gaps. V1 should be implemented as a maintainable product slice, not as one large prototype file.

### Lessons learned

- SQLite is more than sufficient for v1 search, dedupe, conflict tracking, status, and joins. Prefer schema/index/transaction tuning over custom Go data structures.
- Determinism is feasible only when implemented deliberately: pinned test metadata, stable discovery order, canonical manifest encoding, normalized tar metadata, and deterministic compression.
- Prompt reconstruction for images needs occurrence metadata (`entry_assets`-style records), not only image blob metadata. Prompt order and source references matter.
- Adapters must remain source-agnostic at their boundary. Pi and Claude Code already differ enough that archive, ingest, search, and read cannot depend on either format directly.
- A functional thin slice is not the same as a production-quality v1. Realistic fixtures, package boundaries, migrations, and fuzz/property tests are part of v1 quality.

### Decisions now locked by implementation learning

| Area | Decision |
|---|---|
| Package layout | Use packages, not a monolithic `main.go`: `cmd/aha`, `internal/config`, `internal/adapters`, `internal/archive`, `internal/corpus`, `internal/search`, `internal/cli`, `internal/testutil`. |
| CLI framework | Standard library `flag` is acceptable for v1 unless command UX becomes painful; command handlers must be testable via injected `io.Reader`/`io.Writer`. |
| SQLite driver | Use `modernc.org/sqlite` for CGO-free SQLite with FTS5; verify FTS5 in tests. |
| Config parser | Use JSONC via `github.com/tailscale/hujson` or equivalent. |
| Manifest encoding | Use a canonical/stable manifest encoder; do not rely on incidental map iteration order. Struct encoding is acceptable only if all manifest shapes avoid maps or sort map keys explicitly. |
| Schema evolution | Include schema initialisation in code and leave room for migrations/versioning from the first implementation. |
| Image dimensions | Extract dimensions for common image types when bytes are available, using cheap standard-library paths where possible. Keep zero/unknown only when extraction fails. |
| README sync | Test documented commands and privacy warning against the actual command registry, not a hand-maintained list in tests. |

### Implementation quality bar

V1 implementation should satisfy these engineering constraints:

- no single-file implementation beyond small `main.go` command bootstrap;
- no package import cycles;
- command registry is the source of truth for help and doc-sync tests;
- archive creation can be tested without touching user home directories;
- ingest can be tested against a real temporary SQLite database;
- all parser packages have fuzz tests or property-style tests proving they do not panic on arbitrary JSONL;
- deterministic bundle tests compare compressed bundle bytes, not just manifest fields;
- golden fixtures include realistic anonymized Pi and Claude Code sessions, including subagent sessions and image-bearing prompts;
- every bug discovered during implementation receives a regression test before the fix;
- hidden test-only flags, if any, are clearly marked and kept out of normal help unless intentionally supported.

### Known prototype mistakes to avoid

- Do not put all code in one large `main.go`.
- Do not call synthetic-fixture-only validation “done” without a clear limitation.
- Do not claim image dimension support unless dimensions are actually extracted.
- Do not add JSON metadata columns without tests that prove they round-trip and remain queryable when needed.
- Do not merge heterogeneous search result sets without a deterministic global order.
- Do not leave artefact parent linkage completely opaque when source metadata can provide hints.
- Do not rely on docs and tests manually listing commands in separate places.

### Required implementation loop

Implementation should proceed in explicit learning loops:

```txt
update spec with intended behavior and lessons
→ implement only what the spec now says
→ run tests and adversarial review
→ update spec with newly learned decisions, regrets, and fixes
→ repeat until reviewers find no P0/P1 issues and remaining regrets are explicitly accepted or resolved
```

A loop is not complete until spec and implementation agree. If implementation makes an unplanned product or architecture decision, pause and record it in the spec before continuing.

### Second-pass implementation lessons

- Pi session identity must come from the Pi session header `id` when present, not from the filename. Filename-derived IDs are only a fallback.
- Pi artefacts must be preserved even when parent linkage is uncertain. Link only when inferable; otherwise store as unlinked and keep it searchable.
- Unlinked text artefacts need a read path. Search emits canonical `artifact:v1:<sha256>` refs for unlinked artefact results.
- Tool output indexing must be enforced at ingest, not just stated as policy. Only user/assistant/summaries are indexed by default; `toolResult` and `bashExecution` text require explicit opt-in.
- Bundle duplicate semantics must distinguish exact duplicates from same `bundle_id` with different content. Same ID with different SHA is an error/quarantine condition, not a silent duplicate.
- Search/read coherence applies to artefact hits too. Every search result must include enough identity to be readable.
- Archive writing should stream to disk instead of buffering the whole compressed bundle. Ingest should likewise avoid whole-bundle memory loading for large histories.
- Parser robustness should use line reading that records diagnostics instead of failing an entire session on malformed lines or scanner size limits.
- Content-addressed blobs must never be overwritten in place. File and image blobs are written to temporary files and atomically renamed only if the final blob path does not already exist.
- Rejected or corrupt bundles should not be promoted into the corpus bundle store. Stage the incoming bundle, validate manifest-listed file hashes/sizes and archive honesty, then move the staged bundle into `blobs/bundles/`.

### Additional locked decisions from second pass

| Area | Decision |
|---|---|
| Pi session identity | Use header `id` as `source_session_id` when present. |
| Artefact identity | Store artefact occurrences/provenance separately enough that identical bytes from different paths/bundles are not lost. |
| Unlinked artefact read | Search returns canonical `artifact:v1:<sha256>` refs for unlinked artefact hits; `read` accepts them. |
| Artefact parent linkage | Link only with evidence; preserve and index unlinked artefacts. |
| Tool output indexing | Enforce in ingest based on role and `index_tool_output`. |
| Bundle duplicate conflict | Same `bundle_id` with different SHA is an error/conflict, not a no-op. |
| Archive memory use | Avoid all-in-memory archive write/read paths for normal snapshot and ingest. |
| Blob writes | Write content-addressed file/image blobs with temp-file + atomic rename; skip existing blobs. |
| Bundle promotion | Store bundle blobs only after validation succeeds. |

### Third-pass implementation lessons

A further loop clarified process and remaining quality gates:

- The required loop is not ceremonial. The correct sequence is spec update, implementation, adversarial review, spec update with lessons, then repeat until no P0/P1 issues remain and remaining regrets are explicitly resolved or accepted.
- “V1 done” should not be claimed from passing tests alone. It requires a clean adversarial review focused on spec compliance, data loss, determinism, and search/read coherence.
- Raw preservation, queryable provenance, and safe blob lifecycle are distinct requirements. Satisfying one does not imply the others.
- Streaming and atomic writes are core v1 design requirements, not performance polish.
- Every result class returned by search must be readable, including unlinked artefacts.
- Review-discovered issues should become durable spec text and regression tests before continuing.

### Final v1 success condition

V1 is complete only when all of these are true:

- every locked v1 decision and definition-of-done item in this spec has implementation coverage;
- implementation has been rolled through the spec→implement→review→spec loop until adversarial reviewers report no P0/P1 issues;
- `go test ./...`, `go vet ./...`, `go test -race ./...`, parser fuzz tests, and `go build ./cmd/aha` all pass;
- no known data-loss, determinism, provenance, or search/read-coherence regret remains unaddressed;
- any residual limitation is explicitly documented as v2 or non-goal rather than hidden in the implementation.

### Fourth-pass implementation lessons

- CLI examples must be executable as written. If docs show flags after positional query text, the CLI must support that form or the docs must change.
- Text artefacts are documents, not previews. Previews are for display only; FTS must index the full text artefact body when it is valid text.
- Summary indexing must recognise source-native summary types beyond Pi-specific `branchSummary`/`compactionSummary` when a `summary` or text field is present.
- V1 image handling includes both embedded image payloads and image files discovered as artefacts. Non-embedded image references without bytes can be recorded as references, but image files available on disk must be content-addressed as image blobs.
- Claude Code subagent behaviour requires at least one `agent-*.jsonl` fixture and assertion that it is discovered/ingested as a subagent.

### Additional locked decisions from fourth pass

| Area | Decision |
|---|---|
| CLI flags | Commands with positional query/session arguments must accept flags before or after the positional argument when documented examples show that style. |
| Artefact FTS | Index full valid UTF-8 artefact text; keep previews separately for status/display. |
| Summary FTS | Index entries with `branchSummary`, `compactionSummary`, or generic `summary` type when summary/text content is present. |
| Image files | Recognised image files included as artefacts are also stored in `images` as content-addressed blobs with dimensions when possible. |
| Claude subagent fixture | V1 tests include `agent-*.jsonl` and assert subagent ingestion. |

### Fifth-pass implementation lessons

- Full-text artefact indexing implies artefact reads must retrieve full artefact text or a matched context, not only a display preview.
- Supporting flags after query text must not break standard `--` terminator semantics. Users need to search for flag-looking strings such as `--json` and `--path` from historical command transcripts.

### Additional locked decisions from fifth pass

| Area | Decision |
|---|---|
| Artefact read body | `read` for artefact hits returns full text when the artefact is valid UTF-8, with preview remaining only a display/status field. |
| Search query terminator | `aha search -- --literal-flag` treats terms after `--` as query text, not CLI flags. |

### Sixth-pass implementation lessons

- Schema evolution needs real migrations, not only `CREATE TABLE IF NOT EXISTS`. Any added column must be applied idempotently to existing v1 corpora.
- Migration tests should create an older schema shape and verify current startup upgrades it before ingest/read paths use new columns.

### Additional locked decisions from sixth pass

| Area | Decision |
|---|---|
| Schema migration | Keep a migration path for v1 schema changes; use idempotent `ALTER TABLE`/metadata checks where needed. |
| Migration tests | Tests must cover upgrading at least one older corpus shape when schema changes. |

### Seventh-cycle audit lessons

A Git-history plus Pi-session audit clarified progress and process accounting:

- Before the seventh cycle, three implementation attempts had been built, two had been rolled back, and one was current.
- Pi session history showed the loop was real but not initially disciplined: the user had to correct the ordering to `update spec → implement → review → update spec with lessons → repeat`.
- The spec grew materially from the initial draft, but some `Remaining issues` text became stale after implementation decisions were locked. Stale open questions are a spec bug because they make it unclear whether v1 is complete.
- A final no-regrets loop must include spec hygiene: move answered questions into locked decisions and leave only true open/v2 questions.
- Progress should be judged by shrinking P0/P1 regret classes, stronger regression tests, and clearer locked decisions, not merely by commit count or passing tests.

### Additional locked decisions from seventh cycle

| Area | Decision |
|---|---|
| Cycle accounting | Track implementation attempts, rollbacks, and lesson loops in the spec. |
| Spec hygiene | Do not leave answered implementation questions in `Remaining issues`; move them to locked decisions or implemented-state notes. |
| Completion evidence | A clean v1 claim requires Git history, Pi session history, validation commands, and fresh adversarial review to agree that no P0/P1 regrets remain. |
| Progress metric | Progress means fewer unresolved data-loss, determinism, provenance, migration, and search/read-coherence risks after each cycle. |

### Implementation cycle ledger

| Cycle | Implementation attempt | Learning/review result | Rollback? | Resulting state |
|---:|---|---|---|---|
| 1 | `dc227ff` monolithic `aha v1` CLI | Proved core shape, exposed maintainability/spec gaps. | Yes: `ffbc899` | Spec gained implementation lessons; monolith rejected. |
| 2 | `39b3205` maintainable package-based v1 | Better package boundaries; review found hardening gaps. | No immediate rollback | Established current architecture. |
| 3 | `0d78c2a` archive/ingest/blob hardening | Streaming validation and atomic blob lifecycle addressed P1 risks. | No immediate rollback | Hardened implementation baseline. |
| 4 | Final-loop reimplementation after `c43f6d2` rollback | Review found executable-doc, artefact FTS, summary, image-file, and Claude subagent fixture gaps. | Yes: `c43f6d2` before redo | Spec fourth-pass lessons and regression fixes. |
| 5 | Fourth/fifth-pass fixes | Review found artefact search/read incoherence and `--` terminator regression. | No | Full artefact read body and literal flag-query semantics. |
| 6 | Sixth-pass migration fix | Review found existing-corpus schema migration gap. | No | Migration path/test added; reviewers reported clean enough for v1. |
| 7 | Audit/spec-hygiene rollback and redo | Fresh review found a P1: Pi ingest could reopen mutable `raw_path` for header identity instead of using the immutable bundled reader. | Yes: `d857a47` before redo; redo committed as `b5eeff5` but rejected by review | Lesson recorded; must roll back and redo with immutable bundle-derived identity. |
| 8 | Immutable-ingest provenance redo | Fresh review found no P0/P1 after Pi identity-from-bundle fix and regression test. | Yes: `5768b51` before redo | Implementation `9a46ca6`; clean enough until next requested cycles. |
| 9 | Release-readiness redo | Fresh review found a P1: spec cycle counts/ledger were stale after the ninth rollback and redo. | Yes: `ca09ee9` before redo; redo committed as `f2dac7c` but rejected by review | Lesson recorded; cycle accounting needs a regression test. |
| 10 | Ledger-synchronized release redo | Adds a test to keep spec and lessons cycle accounting synchronized. | Required by this loop | Current cycle; stop after fresh review reports no P0/P1. |

### Eighth-cycle implementation lessons

- Ingest must be a pure function of the immutable bundle plus corpus state. It must not reopen `raw_path` live source files while parsing a bundled session.
- Adapter discovery may inspect live files while creating a snapshot, but adapter parsing during ingest must derive source identity and metadata from the `io.Reader` containing bundled bytes.
- Pi header identity is especially sensitive: a changed live session file at the same path can corrupt `source_session_id`, dedupe keys, parent linkage, and conflict behaviour if parse-time code rereads the live path.

### Additional locked decisions from eighth cycle

| Area | Decision |
|---|---|
| Immutable ingest | During bundle ingest, source adapters must not read live source paths for parsed identity; they must use bundled content and manifest metadata. |
| Pi parse identity | Pi `ParseSession` derives the header `id` from the first parsed raw session entry; file-path header reads are allowed only during live discovery/snapshot. |
| Provenance regression | Tests must prove ingest identity comes from bundled Pi content even if `raw_path` points to a different mutable file. |

### Post-audit hardening decisions

| Area | Decision |
|---|---|
| Help behaviour | `aha <command> --help` exits successfully for all subcommands. |
| Image exclusion | `include_images=false` suppresses normalized embedded-image assets and image artefact rows/blobs; raw session files and promoted bundle bytes remain preserved in v1 and may still contain embedded image bytes. Skipped image tar entries are still hash/size validated before bundle acceptance. |
| Symlink safety | Source discovery skips symlinks; snapshot copy rejects symlinks/non-regular files; output and repo paths must not resolve inside configured source roots. |
| Ingest budgets | Bundle ingest uses bounded compressed staging plus manifest, file-count, per-entry, and total-uncompressed limits before spooling/promoting content. |
| Trust verification | Docs must name executable tests that actually verify read-only/no-network/privacy claims. |

### Tenth-cycle process-accounting lessons

- Cycle accounting is itself a release invariant once the spec says it is required.
- A reviewer found stale counts immediately after the ninth redo, proving that manual ledger maintenance is easy to miss.
- Add a doc-sync regression test for cycle counts so the next cycle cannot silently leave spec and lessons out of sync.

### Additional locked decisions from tenth cycle

| Area | Decision |
|---|---|
| Ledger sync test | A test must assert that the spec and lessons document name the current cycle count and current implementation/rollback counts. |
| Cycle count updates | Updating the implementation cycle ledger is part of the spec-update step before each rollback/reimplementation cycle. |

### Current cycle counts

- Implementation attempts built: 7 (`dc227ff`, `39b3205`/`0d78c2a`, final-loop redo ending `6d13867`, seventh-cycle rejected redo `b5eeff5`, eighth-cycle redo `9a46ca6`, ninth-cycle rejected redo `f2dac7c`, tenth-cycle current redo).
- Full implementation rollbacks committed: 6 (`ffbc899`, `c43f6d2`, `d857a47`, `5768b51`, `ca09ee9`, tenth-cycle rollback of `f2dac7c`).
- Lesson/spec-update cycles recorded: 10.
- Current stop condition: fresh reviewers report no P0/P1/no rollback-worthy regrets after the tenth-cycle redo.

### Ninth-cycle release-readiness lessons

- Lessons learned should be a first-class document, not only scattered through commit messages and the spec.
- Schema examples in the spec are compatibility contracts. If implementation adds columns such as `text_body` or subagent markers, the spec must be updated in the same docs-only step before reimplementation.
- Release hardening needs automation. The canonical validation command set should be represented in CI even if large real-corpus validation remains manual.
- README/release docs should state v1 limitations explicitly so accepted regrets are visible to users.

### Additional locked decisions from ninth cycle

| Area | Decision |
|---|---|
| Lessons artefact | Maintain `docs/lessons-learned.md` as the durable cycle-learning document. |
| Schema documentation | Keep the spec schema sketch synchronized with implemented v1 columns that affect compatibility or behaviour. |
| CI | Add a CI workflow for `go test`, `go vet`, race tests, fuzz smoke, build, and whitespace checks. |
| Limitation docs | README must describe accepted v1 limitations, not only features. |
| User journeys | `docs/user-journeys.md` defines the no-flag defaults optimised for local first use, routine refresh, search/read, and imported bundles. |
| Refresh command | `aha archive upload && aha archive download` is the default local aggregation command: snapshot configured sources or reuse unchanged depot state, then ingest pending/new depot bundles into the configured corpus. It supports `--session` and `--max-sessions` for one-to-all local-session scope. |
| Repo alias | Corpus path flags also accept `--workspace` where users are thinking in terms of an aggregation repo. |

### Performance/scalability implementation lessons

- Performance claims need executable guardrails at the cheapest layer: query-plan tests for SQLite shape, fake-driver counters for R2/network cost, property tests for duplicate/catalogue cardinality, and benchmarks for magnitude.
- Routine commands should avoid historical-byte work. Deep integrity remains available, but `status`, no-op `refresh`, quick depot verify, and ordinary search should scale with metadata, indexed rows, or bounded output.
- Search output is an agent cost centre, not only a latency concern. High `--limit` values produce large JSON/ref payloads, so v1 caps requested limits at 200 and asks users/agents to refine with `--source`, `--machine`, `--project`, or `--path-token`.
- Exact project and path-segment workflows need indexed filters. Arbitrary contains matching remains useful but must be documented as a convenience slow path.
- Disk growth is a product behaviour. Because raw preservation is the trust default, deletion is explicit: show size, allow SQLite vacuum, dry-run orphan pruning by default, and leave broader retention/export policies as future product choices.
- Remote/R2 operation cost is part of correctness. Quick verification and status must not download bundles; deep/repair paths must be explicit and report bytes read/downloaded.

### Additional locked decisions from performance/scalability work

| Area | Decision |
|---|---|
| Verify complexity | FTS verification uses rowid-backed identity and query-plan guards; repair remains a full derived-index rebuild. |
| Ingest SQL | Use transaction-scoped prepared statements and prefetch same-session/cross-machine conflict state per session before per-entry inserts. |
| Known blobs | If a content-addressed file blob row/path already exists, ingest must not recompress or rewrite that blob. |
| Search filters | `--project` is exact/indexed; `--path-token` is exact/indexed over derived path-token tables; `--path` is contains/non-indexed. |
| Search limit | Requested search limits above 200 are capped with a user-visible warning. |
| Depot catalogue merge | Repair and compaction use map-backed dedupe by `bundle_sha256`, preserving first non-empty metadata. |
| Depot verification cost | Quick/default verify uses metadata/head/existence checks; `--deep` and `--repair` are the byte-reading modes and report byte counters. |
| Corpus maintenance | `aha workspace status`, `vacuum`, and dry-run/forced `prune-orphans` are the v1 local maintenance surface. |
| Depot maintenance | internal Archive compaction (no public command) deduplicates catalogue refs without reading bundle bytes. |
| Licence | The project is MIT-licensed. |

## Validation plan

### Canonical examples

1. **Single-machine first refresh**
   - Input: local Pi and Claude Code directories.
   - Expected: one bundle, one machine, two sources, searchable corpus.

2. **Scoped local refresh/snapshot**
   - Input: local Pi and Claude Code directories plus `--session MATCH` or `--max-sessions 1`.
   - Expected: bundle/corpus contains only selected local sessions and their linked artefacts.

3. **Same bundle ingested twice**
   - Input: identical archive.
   - Expected: second ingest is a no-op except ingest-attempt audit row.

4. **Later bundle from same machine**
   - Input: prior archive plus a changed/grown session file.
   - Expected: new file version recorded, new entries added, old entries unchanged.

5. **Second machine**
   - Input: bundle from another `machine_id`.
   - Expected: same corpus searches across both machines and can filter by machine.

6. **Indexed search filters**
   - Input: corpus with several project keys and path-token segments.
   - Expected: `aha search --project KEY` and `aha search --path-token TOKEN` return readable refs and query-plan tests show the intended indexes are available to the actual SQL.

7. **Depot cost modes**
   - Input: fake-R2 depot with catalogue refs and bundle objects.
   - Expected: `depot verify` quick uses list/head metadata and downloads zero bundle bytes; `--deep`/`--repair` download bundle bytes and report that cost; `depot compact` rewrites catalogue metadata without touching bundle bytes.

8. **Local maintenance**
   - Input: corpus with database, blobs, and an orphan blob file.
   - Expected: `corpus size` classifies database/blob bytes; `corpus vacuum` preserves data; `corpus prune-orphans` dry-run reports without deleting and `--force` deletes only unreferenced blobs.

### Edge cases

1. **Same session ID, conflicting entry body**
   - Expected: conflict row, no overwrite.

2. **Active file changes during copy**
   - Expected: retry once, then mark `copy_state=unstable` if still changing.

3. **Large tool output / image content**
   - Expected: preserve raw entry, do not index tool output, avoid base64 snippets.

4. **Unlinked subagent artefact**
   - Expected: preserve and index as artefact with `parent_session_id = null`.

5. **Image-bearing prompt recreation**
   - Input: session entries containing text plus embedded or referenced images.
   - Expected: raw entry preserved, image bytes stored by hash, prompt order recorded, and reconstructed prompt matches source-native text/image order.

6. **Malformed and unknown Claude JSONL entries**
   - Input: valid messages mixed with malformed JSON, unknown `type`, unknown content block types, invalid timestamps, and Unicode text.
   - Expected: raw file preserved, diagnostics recorded, known text indexed, parser does not crash.

7. **Deterministic bundle fixture**
   - Input: stable testdata tree with pinned capture metadata.
   - Expected: stable manifest JSON, tar entry order, tar metadata, and compressed bundle bytes.

8. **Immutable ingest identity**
   - Input: bundle containing a Pi session header `id=A` whose manifest `raw_path` points to a live file now containing header `id=B`.
   - Expected: ingest records session identity `A` from the bundled bytes and never consults the mutable live file during parse.

## Open question and limitation ledger

This section contains no hidden v1 blockers. Each item is classified as a locked v1 behaviour, post-v1 release-hardening task, or v2/non-goal.

### Locked v1 behaviours, not open blockers

- Claude Code default root is `~/.claude/projects/` unless configured.
- Claude Code entry IDs use source-native IDs when present and stable derived IDs otherwise.
- Claude `agent-*.jsonl` files are ingested as subagent sessions; parent linkage is recorded only with evidence.
- V1 preserves raw paths and derives a simple `project_key`; advanced path grouping is not required for v1.
- V1 preserves tool output in raw files but does not index it by default.
- V1 `read` shows surrounding file-order context rather than source-native branch/thread reconstruction.
- Conflicts are quarantined and never overwrite existing entries.
- V1 preserves raw entry JSON, image blobs, prompt order, content indexes, references, and dimensions when available.

### Post-v1 release hardening, not implementation blockers

- Validate perception and constants against large real corpora; package-level pathological benchmarks and query-plan/operation-count guards already cover the algorithmic risks.
- Add more anonymized real-world Claude Code fixtures, especially image/attachment representations and active append-only files.
- Run real R2 integration against a scratch bucket before any R2-focused release announcement.
- Prepare release notes that repeat the v1 privacy warning, local/R2 cost modes, and accepted limitations.

### V2 or later

- Secret redaction and public dataset preparation.
- Windows support, including `C--...` drive-letter project directories, path handling, UTF-8 terminal output, and filesystem permission tests.
- Configurable project path rewrite/grouping rules for monorepos, renamed folders, and cross-machine aliases.
- Opt-in tool-output indexing.
- Source-native branch/thread reconstruction in `read`.
- Conflict display/search policy refinements.
- True multi-row ingest insert batching if benchmarks show SQLite step overhead dominates after prepared statements/prefetching.
- Depot catalogue summary files or local stale status caches if raw catalogue scans become a real bottleneck after compaction.
- Retention/export/delete policies beyond explicit orphan pruning.
- OCR/captioning for image content.

## Relationship to adjacent tools

| Tool | Useful idea | Difference |
|---|---|---|
| `geekmuse/chronicle` | Multi-machine session sync, path canonicalization, Git backend, CRDT merge | This spec snapshots immutable bundles and ingests them into a corpus rather than syncing live files. |
| `samfoy/pi-session-search` | FTS/hybrid session search, session read/list tools | This spec ingests dated bundles from multiple machines and sources. |
| `samfoy/pi-total-recall` | Session search as part of a context stack | Broader memory stack; not snapshot-bundle archival. |
| `MohammadErfan-Jabbari/pi-session-inspect` | Read-only local session inspection | Local inspection rather than cross-machine bundle ingest. |
| `Dwsy/pi-session-manager` | Session manager UI, SQLite search, external sessions | Heavier workbench; not a Go CLI archive format. |
| `badlogic/pi-share-hf` | Incremental collection, redaction, review, upload | Public dataset pipeline; aha keeps raw bundles private and can redact corpus projections with `redaction:"v1"`. |
| `adewale/claude-history-explorer` | Concrete Claude Code JSONL discovery/parsing, `agent-*.jsonl`, rich read-only UX, verifiable trust model | Single-machine Python explorer rather than multi-machine immutable bundle corpus; see `docs/comparisons/claude-history-explorer.md`. |
| `adewale/testing-best-practices` | Table-driven Go tests, real fixtures, golden files, property/fuzz tests, doc-sync tests, test-quality antipatterns | Testing guidance rather than an agent-history product. |

Remembered line:

> The bundle is the durable evidence. The corpus is the index.
