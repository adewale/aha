---
title: Agent History Aggregator Spec
kind: spec
created: 2026-05-18
updated: 2026-05-18
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
source_count: 4
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
| Source adapters in v1 | Pi and Claude Code only |
| Compression/archive format | `tar.zst` |
| Paths | Preserve raw paths by default |
| SQLite implementation | Pure Go portability preferred |
| Secrets/redaction | Punt to v2; v1 warns but does not redact |
| Subagents | Include in v1 |
| Images | Include in v1 |
| Live sync | No |
| Live session mutation | Never |
| Embeddings | Not v1 |
| Tool output | Preserve in raw files; ignore for v1 search/indexing |

## Non-goals

V1 does not try to:

- mutate live Pi or Claude Code session files;
- continuously sync sessions between machines;
- resume a session from another machine;
- publish or redact a public dataset;
- solve secret handling;
- inject old session history into every new agent run;
- build a hosted service.

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

The CLI should keep source-specific parsing behind adapters.

```go
type SourceAdapter interface {
    Name() string
    Discover(config SourceConfig) ([]SessionFile, error)
    ParseSession(path string, r io.Reader) (*ParsedSession, error)
    DiscoverArtifacts(session SessionFile) ([]ArtifactFile, error)
}
```

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

The Claude Code adapter must answer these implementation questions during development:

- default session directory on macOS, Linux, and Windows;
- session file format and version fields;
- how project paths are encoded;
- how subagent work is represented;
- how images and attachments are stored;
- whether session files can be appended while Claude Code is running;
- whether message IDs are stable enough for entry-level dedupe.

Until the adapter is verified against real Claude Code files, the parser should be conservative: preserve raw files, normalize only fields it can prove.

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

The archive should be deterministic where practical:

- stable file ordering;
- stable manifest ordering;
- normalized tar metadata where safe;
- bundle SHA-256 computed after archive creation.

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

Claude Code subagent artifact handling remains an adapter verification task.

## Images

V1 includes images.

Image handling rules:

- preserve image files and image payloads;
- content-address image blobs by SHA-256;
- index metadata such as filename, source, MIME type, size, dimensions when cheaply available, parent session, and entry ID;
- avoid dumping base64 image bodies into FTS snippets;
- extract embedded base64 images into image blobs during ingest while preserving the raw entry JSON.

V1 does not require OCR or image captioning.

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

Use SQLite plus filesystem blobs.

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

SQLite should be pure-Go portable. Candidate drivers need FTS5 verification before implementation is locked.

Possible tables:

```sql
bundles(bundle_id, bundle_sha256, machine_id, captured_at, ingested_at, manifest_json)
machines(machine_id, first_seen_at, last_seen_at, labels_json)
sources(source_id, source_name, adapter_version)
files(file_sha256, kind, bytes, compressed_blob_path, first_seen_bundle_id)
sessions(session_key, source_name, source_session_id, machine_id, raw_cwd, started_at)
session_versions(session_key, file_sha256, bundle_id, relative_path, raw_path, observed_at, copy_state)
entries(session_key, entry_id, parent_id, entry_type, timestamp, role, entry_sha256, raw_json)
messages(session_key, entry_id, role, text, tool_name, command, files_json, model, provider, tokens, cost)
artifacts(artifact_sha256, source_name, kind, parent_session_key, parent_entry_id, raw_path, relative_path, text_preview)
images(image_sha256, source_name, parent_session_key, parent_entry_id, mime_type, bytes, width, height, raw_path)
conflicts(conflict_id, session_key, entry_id, first_entry_sha256, second_entry_sha256, details_json)
```

Search table:

```sql
fts_messages(session_key, entry_id, text)
```

For v1, store raw source entries and normalized fields. Derived columns can be regenerated.

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

- SQLite FTS BM25;
- support recency/date filtering;
- no semantic embeddings;
- no tool-output ranking because tool output is not indexed in v1.

Do not let embeddings hide bad ingestion. The v1 order is:

```txt
parse correctly
→ dedupe safely
→ FTS search works
→ add metadata filters
→ consider embeddings later
```

## Privacy and v2 redaction

V1 punts secret handling.

The CLI and README must say plainly:

```txt
V1 does not redact secrets. Bundles may contain credentials, private prompts, source code, tool output, images, filesystem paths, and API responses. Do not upload bundles or corpora publicly unless you have reviewed them yourself.
```

V2 can add:

- configured secret files;
- deny patterns;
- common token regexes;
- TruffleHog-style scanning;
- redacted derivative bundles;
- public dataset export.

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
| SQLite | pure-Go driver with FTS5 support verified |
| Config | platform-native config dir, likely TOML or JSON |
| Output | human table by default, `--json` for scripts |

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

## Remaining issues to resolve

### Claude Code adapter verification

- Confirm default session directories on macOS, Linux, and Windows.
- Confirm session file format and stable IDs.
- Confirm how project paths are represented.
- Confirm subagent transcript/artifact representation.
- Confirm image/attachment representation.
- Confirm whether active files are append-only and safe to copy with retry.

### Pure-Go SQLite and FTS5

- Choose a pure-Go SQLite driver.
- Verify FTS5 support in distributed binaries.
- Confirm acceptable performance for large corpora.
- Decide whether to use SQLite FTS5 or a separate pure-Go search index if FTS5 portability is poor.

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

### Image depth

- V1 preserves and metadata-indexes images.
- OCR and captioning are out of v1.
- Open question: should image dimensions be extracted in v1, or should v1 only store MIME/bytes/hash/path?

### Config format

- Need to choose TOML, JSON, or YAML.
- Need platform-native config path.
- Need first-run config initialization behavior.

### Archive determinism

- Need exact tar metadata normalization rules.
- Need to decide whether deterministic bundles are a hard requirement or best-effort.

### Windows support

- Go can support Windows, but v1 acceptance target is macOS/Linux unless otherwise decided.
- Need path handling tests before promising Windows support.

## Relationship to adjacent tools

| Tool | Useful idea | Difference |
|---|---|---|
| `geekmuse/chronicle` | Multi-machine session sync, path canonicalization, Git backend, CRDT merge | This spec snapshots immutable bundles and ingests them into a corpus rather than syncing live files. |
| `samfoy/pi-session-search` | FTS/hybrid session search, session read/list tools | This spec ingests dated bundles from multiple machines and sources. |
| `samfoy/pi-total-recall` | Session search as part of a context stack | Broader memory stack; not snapshot-bundle archival. |
| `MohammadErfan-Jabbari/pi-session-inspect` | Read-only local session inspection | Local inspection rather than cross-machine bundle ingest. |
| `Dwsy/pi-session-manager` | Session manager UI, SQLite search, external sessions | Heavier workbench; not a Go CLI archive format. |
| `badlogic/pi-share-hf` | Incremental collection, redaction, review, upload | Public dataset pipeline; v1 here is private local corpus and does not redact. |

Remembered line:

> The bundle is the receipt. The corpus is the index.
