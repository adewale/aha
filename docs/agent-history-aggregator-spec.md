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
| Secrets/redaction | Punt to v2; v1 warns but does not redact |
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

The tool is a local archive and search system. Users should treat bundles and corpora as private because v1 does not redact secrets.

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
| `pi` | Pi JSONL sessions and related subagent artifacts. |
| `claude-code` | Claude Code sessions and related subagent/image artifacts. |

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
- bundle SHA-256 computed after archive creation and written to a receipt outside the bundle;
- tests pin `captured_at`, `bundle_id`, and machine/config values to prove deterministic fixtures.

## Manifest

`manifest.json` identifies the bundle and lets the ingester verify contents without trusting the filename.

```json
{
  "schema": "agent-session-snapshot-bundle/v1",
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

Do not silently use raw hostname as `machine_id`. The first run should require either a config value or a CLI flag:

```bash
aha snapshot --machine ade-mbp
```

Raw paths are preserved by default because they are useful for search and project reconstruction. Hostname and username should be hashed by default if stored in the manifest.

## Snapshot command

Example:

```bash
aha snapshot \
  --machine ade-mbp \
  --source pi=$HOME/.pi/agent/sessions \
  --source claude-code=$HOME/.claude/projects \
  --out ~/agent-session-bundles/
```

Responsibilities:

1. Read config and CLI flags.
2. Resolve source directories.
3. Discover sessions, subagent artifacts, and images through source adapters.
4. Copy live files safely without modifying them.
5. Compute SHA-256 for every file.
6. Parse enough metadata to build the manifest.
7. Write deterministic `manifest.json`.
8. Write `tar.zst` bundle.
9. Compute bundle SHA-256.
10. Write a local receipt.

V1 does not redact secrets. The command should print a direct warning unless a non-interactive flag accepts it:

```txt
V1 does not redact secrets. Bundles may contain prompts, source code, tool output, images, tokens, and private paths. Treat the bundle as private.
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

## Subagents and artifacts

V1 includes subagent artifacts.

Artifact handling rules:

- include adapter-recognized subagent sidecar files;
- preserve raw artifact files;
- compute content hashes;
- link artifacts to parent sessions when inferable;
- keep unlinked artifacts with `parent_session_id = null`;
- index text artifacts as documents;
- do not throw away artifacts just because linkage is uncertain.

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
aha ingest ~/agent-session-bundles/aha-sessions-*.tar.zst
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
6. Store artifacts and images by content hash.
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

| Case | Behavior |
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
    files/{file_sha256}.zst
    images/{image_sha256}.{ext}
    artifacts/{artifact_sha256}
  reports/
```

SQLite driver choice is still an implementation detail, but v1 assumes SQLite + FTS5. Prefer a pure-Go driver if it provides reliable FTS5 and JSON support in distributed binaries.

Possible tables:

```sql
bundles(bundle_id, bundle_sha256, machine_id, captured_at, ingested_at, manifest_json)
machines(machine_id, first_seen_at, last_seen_at, labels_json)
sources(source_id, source_name, adapter_version, capabilities_json)
files(file_sha256, kind, bytes, compressed_blob_path, first_seen_bundle_id)
sessions(session_key, source_name, source_session_id, machine_id, raw_cwd, project_key, started_at, source_metadata_json)
session_versions(session_key, file_sha256, bundle_id, relative_path, raw_path, observed_at, copy_state)
entries(session_key, entry_id, parent_id, entry_type, timestamp, role, entry_sha256, raw_json, source_metadata_json)
messages(session_key, entry_id, role, text, tool_name, command, files_json, model, provider, tokens, cost)
artifacts(artifact_id, artifact_sha256, source_name, machine_id, bundle_id, kind, parent_session_key, parent_entry_id, raw_path, relative_path, text_preview)
images(image_sha256, source_name, mime_type, bytes, width, height, ext, blob_path)
entry_assets(session_key, entry_id, asset_sha256, asset_kind, content_index, prompt_order, raw_ref, mime_type, metadata_json)
conflicts(conflict_id, session_key, entry_id, first_entry_sha256, second_entry_sha256, details_json)
```

Search table:

```sql
fts_messages(session_key, entry_id, text)
fts_artifacts(artifact_id, text)
```

For v1, store raw source entries and normalized fields. Derived columns can be regenerated. `entry_assets` links images and other prompt assets back to the exact entry and content position needed to reconstruct prompts.

## Search command

Examples:

```bash
aha search "loadable ephemeral context"
aha search "dynamic workflows" --machine ade-mbp --after 2026-05-01
aha search "xampler/workflows.py" --source pi
aha read --session 019e0d47-ae0f --entry a1b2c3d4 --before 3 --after 5
aha status
aha conflicts
```

Minimum search features:

| Feature | Query example |
|---|---|
| Full-text message search | `dynamic workflows durable streams` |
| Source filter | `--source pi` or `--source claude-code` |
| Machine filter | `--machine ade-mbp` |
| Date filter | `--after 2026-05-01 --before 2026-05-18` |
| Role filter | `--role user` |
| File/path filter | `--path xampler/workflows.py` |
| JSON output | `--json` |
| Session read | bounded context around matching entries |

Default search indexes user and assistant text, summaries, text artifacts, and selected source metadata. V1 preserves raw tool output inside the stored source files but does not index tool output for search.

Default result fields:

```txt
score timestamp source machine project role snippet session_key entry_id
```

## Ranking

V1 ranking:

- SQLite FTS5 BM25;
- ordinary SQLite indexes for source, machine, role, project, path, and date filters;
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
V1 does not redact secrets. Bundles may contain credentials, private prompts, source code, tool output, images, filesystem paths, and API responses. Do not upload bundles or corpora publicly unless you have reviewed them yourself.
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
    { "type": "pi", "root": "~/.pi/agent/sessions", "enabled": true },
    { "type": "claude-code", "root": "~/.claude/projects", "enabled": true }
  ],

  "corpus_dir": "~/.aha",
  "bundle_out_dir": "~/agent-session-bundles",
  "path_mode": "raw",
  "include_subagents": true,
  "include_images": true,
  "index_tool_output": false,
  "redaction": "none-v1"
}
```

CLI flags override config values. The first-run UX should create or print a starter JSONC config, but must not infer `machine_id` silently from raw hostname.

## CLI shape

```txt
aha snapshot
aha ingest
aha search
aha read
aha status
aha conflicts
aha doctor
```

Implementation preferences:

| Concern | Preference |
|---|---|
| CLI framework | `cobra` or standard library; decide during implementation |
| Compression | pure-Go Zstandard |
| Archive | Go stdlib `archive/tar` |
| SQLite | SQLite + FTS5 as the query engine; prefer pure-Go driver if FTS5/JSON support is reliable |
| Config | JSONC in platform-native config dir |
| Output | human table by default, `--json` for scripts |
| Testing | Go stdlib `testing`, table-driven tests, `testdata/` fixtures, property/fuzz tests for parsers, golden-file tests for manifests/search output |

## Thin slice

The first useful implementation should avoid embeddings, sync, redaction, and GUI work.

### Input

- one Pi session directory;
- one Claude Code session directory;
- one machine ID, e.g. `ade-mbp`.

### Processing

- create one `.tar.zst` bundle with `manifest.json`;
- include sessions, subagent artifacts, and images;
- ingest into local SQLite;
- build FTS over user/assistant text, summaries, text artifacts, and selected metadata;
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
- `status` reports machines, bundles, sources, sessions, entries, artifacts, images, index size, and conflicts.
- README states that v1 does not redact secrets.
- Test suite includes realistic Pi and Claude Code fixtures, including `agent-*.jsonl` and image-bearing prompts.
- Test suite proves snapshot read-only behavior, ingest idempotence, deterministic manifests and compressed bundles, conflict quarantine, parser robustness/fuzz safety, prompt image reconstruction including dimensions when available, and search/read coherence.
- CI runs `go test ./...`, `go test -race ./...`, `go vet ./...`, parser fuzz/property tests, deterministic archive tests, and documentation-code sync checks.

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
| Smoke | `aha --help`, `aha snapshot --help`, `aha ingest --help`, `aha search --help`, and a tiny snapshot→ingest→search flow all run. |
| Unit | Path discovery, JSONL parsing, manifest validation, hash calculation, dedupe keys, FTS query construction. |
| Golden files | Deterministic `manifest.json`, ingest reports, search output, `read` output, conflict reports. |
| Property/fuzz | Parsers and normalizers never panic on arbitrary input; archive write/read roundtrips; ingest is idempotent; deterministic snapshot output is stable for stable inputs. |
| Characterization | Realistic anonymized Pi and Claude Code JSONL fixtures capture current source behavior before parser refactors. |
| Regression | Every bug gets a named test first; record the Windows Claude project discovery issue from `claude-history-explorer#8` as a v2 fixture, not a v1 support promise. |
| Documentation sync | README command list, flags, config keys, and privacy warning stay in sync with the actual CLI registry/config structs. |
| Race/concurrency | Active-file copy retry behavior and parallel ingest/search safety pass `go test -race`. |
| Performance | Large-session parser benchmarks catch obvious O(n²) or whole-file-loading regressions. |

### Core properties to test

- **Read-only snapshot:** snapshot never modifies source session files; source mtimes and hashes remain unchanged in stable tests.
- **Archive roundtrip:** decompressing a bundle yields the same manifest and file hashes that snapshot reported.
- **Manifest honesty:** every manifest file entry exists in the archive and every archived session/artifact appears in the manifest.
- **Determinism:** with pinned capture time, bundle ID, file mtimes, and input order, manifest bytes, tar entry ordering, tar metadata, and compressed bundle bytes are stable.
- **Idempotent ingest:** ingesting the same bundle twice does not duplicate logical bundles, files, sessions, entries, messages, artifacts, or images.
- **Append-only merge:** ingesting a later session version adds new entries and never deletes old entries.
- **Conflict safety:** same session/entry ID with different entry hash creates a conflict row and never overwrites the existing entry.
- **Parser robustness:** malformed JSONL lines, unknown roles, unknown block types, invalid timestamps, huge tool outputs, and Unicode text do not crash parsing.
- **Claude discovery:** Unix-style `-Users-...` project directories are discovered if they contain JSONL files; hidden dirs are ignored. Windows-style `C--Users-...` stays in v2 fixtures.
- **Prompt reconstruction:** image-bearing prompts can be recreated from raw entry JSON plus `entry_assets` and image blobs.
- **Search/read coherence:** every search hit can be passed to `aha read` and returns bounded context containing the hit entry.

### Test quality rules

- Avoid mocks for filesystem, tar/zstd, and SQLite behavior; use real temp dirs, real archives, and real databases.
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
| Schema evolution | Include schema initialization in code and leave room for migrations/versioning from the first implementation. |
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
- Do not leave artifact parent linkage completely opaque when source metadata can provide hints.
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
- Pi artifacts must be preserved even when parent linkage is uncertain. Link only when inferable; otherwise store as unlinked and keep it searchable.
- Unlinked text artifacts need a read path. V1 uses synthetic read keys of the form `artifact:<sha256>` for unlinked artifact search results.
- Tool output indexing must be enforced at ingest, not just stated as policy. Only user/assistant/summaries are indexed by default; `toolResult` and `bashExecution` text require explicit opt-in.
- Bundle duplicate semantics must distinguish exact duplicates from same `bundle_id` with different content. Same ID with different SHA is an error/quarantine condition, not a silent duplicate.
- Search/read coherence applies to artifact hits too. Every search result must include enough identity to be readable.
- Archive writing should stream to disk instead of buffering the whole compressed bundle. Ingest should likewise avoid whole-bundle memory loading for large histories.
- Parser robustness should use line reading that records diagnostics instead of failing an entire session on malformed lines or scanner size limits.
- Content-addressed blobs must never be overwritten in place. File and image blobs are written to temporary files and atomically renamed only if the final blob path does not already exist.
- Rejected or corrupt bundles should not be promoted into the corpus bundle store. Stage the incoming bundle, validate manifest-listed file hashes/sizes and archive honesty, then move the staged bundle into `blobs/bundles/`.

### Additional locked decisions from second pass

| Area | Decision |
|---|---|
| Pi session identity | Use header `id` as `source_session_id` when present. |
| Artifact identity | Store artifact occurrences/provenance separately enough that identical bytes from different paths/bundles are not lost. |
| Unlinked artifact read | Search returns `artifact:<sha256>` session keys for unlinked artifact hits; `read` accepts them. |
| Artifact parent linkage | Link only with evidence; preserve and index unlinked artifacts. |
| Tool output indexing | Enforce in ingest based on role and `index_tool_output`. |
| Bundle duplicate conflict | Same `bundle_id` with different SHA is an error/conflict, not a no-op. |
| Archive memory use | Avoid all-in-memory archive write/read paths for normal snapshot and ingest. |
| Blob writes | Write content-addressed file/image blobs with temp-file + atomic rename; skip existing blobs. |
| Bundle promotion | Store bundle blobs only after validation succeeds. |


## Validation plan

### Canonical examples

1. **Single-machine first snapshot**
   - Input: local Pi and Claude Code directories.
   - Expected: one bundle, one machine, two sources, searchable corpus.

2. **Same bundle ingested twice**
   - Input: identical archive.
   - Expected: second ingest is a no-op except ingest-attempt receipt.

3. **Later bundle from same machine**
   - Input: prior archive plus a changed/grown session file.
   - Expected: new file version recorded, new entries added, old entries unchanged.

4. **Second machine**
   - Input: bundle from another `machine_id`.
   - Expected: same corpus searches across both machines and can filter by machine.

### Edge cases

1. **Same session ID, conflicting entry body**
   - Expected: conflict row, no overwrite.

2. **Active file changes during copy**
   - Expected: retry once, then mark `copy_state=unstable` if still changing.

3. **Large tool output / image content**
   - Expected: preserve raw entry, do not index tool output, avoid base64 snippets.

4. **Unlinked subagent artifact**
   - Expected: preserve and index as artifact with `parent_session_id = null`.

5. **Image-bearing prompt recreation**
   - Input: session entries containing text plus embedded or referenced images.
   - Expected: raw entry preserved, image bytes stored by hash, prompt order recorded, and reconstructed prompt matches source-native text/image order.

6. **Malformed and unknown Claude JSONL entries**
   - Input: valid messages mixed with malformed JSON, unknown `type`, unknown content block types, invalid timestamps, and Unicode text.
   - Expected: raw file preserved, diagnostics recorded, known text indexed, parser does not crash.

7. **Deterministic bundle fixture**
   - Input: stable testdata tree with pinned capture metadata.
   - Expected: stable manifest JSON, tar entry order, tar metadata, and compressed bundle bytes.

## Remaining issues to resolve

### Claude Code adapter verification

- Confirm exact default session directory on Linux for current Claude Code versions.
- Confirm whether stable message UUID fields are present in all entries.
- Confirm whether project path decoding can be made reliable enough for display, while preserving raw encoded names as identity metadata.
- Confirm whether `agent-*.jsonl` files contain parent-session linkage or only filename-level subagent identity.
- Confirm image/attachment representation.
- Confirm whether active files are append-only and safe to copy with retry.

### SQLite driver and schema tuning

- Choose a SQLite driver; prefer pure-Go if FTS5 and JSON support are reliable in distributed binaries.
- Verify FTS5 and JSON support in release builds.
- Confirm acceptable performance for large corpora with realistic indexes and transactions.
- Tune schema, indexes, pragmas, and transaction boundaries before considering any non-SQLite component.

### Project identity

- Raw paths are preserved by default.
- Need a derived `project_key` for grouping the same repo across machines.
- Candidate v1 heuristic: basename of `cwd` plus source adapter.
- Open question: should users configure path rewrite/grouping rules in v1?

### Tool-output policy

- V1 ignores tool output for indexing and ranking.
- Raw source files are still preserved, so tool output remains recoverable through session reads or direct bundle inspection.
- Future question: should a later version add opt-in tool-output indexing for users who want command logs and tool results searchable?

### Read semantics

- V1 can show surrounding file-order context.
- Pi entries have tree structure via `parentId`; Claude Code may have different threading semantics.
- Open question: should `read` reconstruct source-native branches in v1 or defer to v1.1?

### Conflict visibility

- Conflicts should be quarantined and never overwritten.
- Open question: should conflicting entries be excluded from default search or shown with a conflict marker?

### Image prompt reconstruction details

- V1 preserves and metadata-indexes images.
- V1 must store enough ordering/reference metadata to recreate image-bearing prompts.
- OCR and captioning are out of v1.
- Need exact source-specific reconstruction rules for Pi and Claude Code content blocks.

### Config initialization

- JSONC is chosen.
- Need exact platform-native config path fallback behavior.
- Need first-run config initialization behavior.

### Archive determinism details

- Deterministic output is required.
- Need exact tar metadata normalization rules and canonical JSON encoder behavior documented in implementation tests.

### Windows support in v2

- Windows support is punted to v2.
- Keep known Windows Claude Code path facts as v2 fixtures, especially `C--...` drive-letter project directories.
- Before claiming Windows support, add path handling, UTF-8 terminal output, and filesystem permission tests.

## Relationship to adjacent tools

| Tool | Useful idea | Difference |
|---|---|---|
| `geekmuse/chronicle` | Multi-machine session sync, path canonicalization, Git backend, CRDT merge | This spec snapshots immutable bundles and ingests them into a corpus rather than syncing live files. |
| `samfoy/pi-session-search` | FTS/hybrid session search, session read/list tools | This spec ingests dated bundles from multiple machines and sources. |
| `samfoy/pi-total-recall` | Session search as part of a context stack | Broader memory stack; not snapshot-bundle archival. |
| `MohammadErfan-Jabbari/pi-session-inspect` | Read-only local session inspection | Local inspection rather than cross-machine bundle ingest. |
| `Dwsy/pi-session-manager` | Session manager UI, SQLite search, external sessions | Heavier workbench; not a Go CLI archive format. |
| `badlogic/pi-share-hf` | Incremental collection, redaction, review, upload | Public dataset pipeline; v1 here is private local corpus and does not redact. |
| `adewale/claude-history-explorer` | Concrete Claude Code JSONL discovery/parsing, `agent-*.jsonl`, rich read-only UX | Single-machine Python explorer rather than multi-machine immutable bundle corpus. |
| `adewale/testing-best-practices` | Table-driven Go tests, real fixtures, golden files, property/fuzz tests, doc-sync tests, test-quality antipatterns | Testing guidance rather than an agent-history product. |

Remembered line:

> The bundle is the receipt. The corpus is the index.
