# OpenCode adapter research

Status: implemented (see below) — originally viable with caveats.

## Implementation note

A read-only `opencode` adapter now ships. Rather than generalize the snapshot
pipeline to ingest SQLite directly (the pipeline assumes one JSONL file per
session, parsed from an `io.Reader`, with raw bytes copied into the bundle), the
adapter converts the database to deterministic, lossless JSONL *during
`Discover`* and lets the unchanged downstream handle the JSONL:

- The DB and any `-wal`/`-shm` sidecars are copied into a stable per-database
  export directory before reading, giving a consistent view of a live WAL
  database without writing to the source.
- Each `session`/`message`/`part` row is dumped with its full `data` JSON
  preserved verbatim (`json.RawMessage`), so the conversion is lossless rather
  than a lossy projection. Output is byte-stable for identical DB contents, so
  the snapshot unchanged-state fingerprint still avoids re-uploading.
- The file-writing lives in `internal/opencodeexport`, not `internal/adapters`,
  so the source adapters keep their textual read-only invariant.

Remaining follow-up: the fixtures are schema-tolerant but synthetic; verify the
`message`/`part` `data` shapes against a real current `anomalyco/opencode`
install (images, subtask/agent parts, compaction summaries, fork/parent
semantics) and commit a real-DB fixture. Run `scripts/smoketest.sh opencode` on a
machine with OpenCode for a safe, read-only end-to-end check (it dumps the live
schema when `sqlite3` is present, which is the fastest way to confirm the
table/column assumptions hold). The notes below capture the original research.

## Scope

This note covers two related projects named OpenCode:

- `anomalyco/opencode` / `opencode.ai`: current high-signal candidate.
- `opencode-ai/opencode`: older Go implementation that also has local SQLite storage.

`aha` should target `anomalyco/opencode` first unless fixtures from current users prove otherwise.

## Evidence: anomalyco/opencode

Repository inspected: `https://github.com/anomalyco/opencode`.

Relevant findings from source:

- Global paths use XDG directories in `packages/core/src/global.ts`:
  - app name: `opencode`;
  - data path: `path.join(xdgData!, "opencode")`;
  - config path: `path.join(xdgConfig!, "opencode")`;
  - state path: `path.join(xdgState!, "opencode")`.
  - On typical Linux/macOS XDG setups this means the main data directory is usually `~/.local/share/opencode` unless `XDG_DATA_HOME` is set.
- Current storage is SQLite in `packages/opencode/src/storage/db.ts`:
  - default DB path is `Global.Path.data/opencode.db`;
  - non-standard release channels may use `opencode-<channel>.db`;
  - `OPENCODE_DB` can override the DB path;
  - SQLite is opened with WAL, busy timeout, and migrations.
- Main schema is in `packages/opencode/src/session/session.sql.ts` and migrations under `packages/opencode/migration/`:
  - `project` table: worktree/project metadata;
  - `session` table: session metadata, parent session, title, model/agent/tokens/cost over time;
  - `message` table: one row per message with JSON `data`;
  - `part` table: one row per message part/tool/text/file/etc. with JSON `data`;
  - `todo`, `permission`, `session_share` tables exist too.
- Earlier JSON-file storage existed and is migrated in `packages/opencode/src/storage/storage.ts` / `json-migration.ts`:
  - old tree under `Global.Path.data/storage`;
  - session JSON under `storage/session/...`;
  - message JSON under `storage/message/...`;
  - part JSON under `storage/part/...`.

## Adapter implications

A read-only adapter is feasible:

1. Discover likely roots:
   - `$OPENCODE_DB` if absolute or relative to XDG data when set;
   - `$XDG_DATA_HOME/opencode/opencode.db`;
   - fallback `~/.local/share/opencode/opencode.db`;
   - channel DBs matching `opencode-*.db`;
   - legacy JSON tree under `<data>/storage`.
2. For SQLite, copy the DB plus WAL/SHM sidecars to a temporary snapshot location before parsing, or open through SQLite immutable/read-only URI only after ensuring WAL visibility. Copying is more consistent with `aha`'s existing snapshot-first design.
3. Parse `project`, `session`, `message`, and `part` rows. Message text likely requires combining message `data` with related `part.data` rows; tool/file/agent/subtask parts should be preserved and selectively indexed like other adapters.
4. Preserve raw DB or raw row JSON in the bundle. Do not write to OpenCode DB.

## Risks / unknowns

- Format is actively migrating: there is a JSON-to-SQLite migration path and many recent DB migrations. The adapter should be fixture-driven and schema-tolerant.
- SQLite live-read consistency matters because OpenCode uses WAL. Snapshot by copying DB/WAL/SHM or by using SQLite backup API if added later.
- `OPENCODE_DB` and release-channel DB names mean one hard-coded path is not enough.
- Need real user fixtures to verify current `message.data` / `part.data` shapes, especially images, tool outputs, subtask/agent parts, compaction summaries, and fork/parent semantics.
- `opencode-ai/opencode` stores a different SQLite schema and should be considered a separate legacy adapter only if users still have that data.

## Recommendation

OpenCode is a good v1.1/v1.2 adapter candidate, after Aider/Cline only if prioritizing JSONL/plain-file simplicity. It is more complex than Codex because current storage is SQLite plus JSON columns, but it is local, discoverable, and read-only-compatible.

Recommended next step: add a read-only `opencode` adapter behind fixtures from a current `anomalyco/opencode` install, starting with SQLite DB copy + query parser and no legacy support until fixtures justify it.
