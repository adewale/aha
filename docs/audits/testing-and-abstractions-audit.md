# Testing and abstraction audit

Date: 2026-05-21

*Historical: predates depot v2 ([../depot-v2-spec.md](../depot-v2-spec.md)). Bundle/catalog depot mechanics referenced below were replaced by content-addressed snapshots in June 2026.*

Inputs:

- `github.com/adewale/testing-best-practices` installed locally as a Pi skill and reviewed.
- Existing adewale Go repo guidance reviewed from `rogue_planet` and `olsen`, especially assertion-quality audits, doc/code consistency audits, real-object tests, and boundary-condition lessons.
- Fresh codebase review by scout/reviewer subagents.

## Changes made from the audit

- Added machine-readable CLI error contracts and regression tests for flag parse errors.
- Added generated `docs/commands.md` from command metadata and a sync test.
- Added golden tests for human/JSON/refs/files/Markdown rendering.
- Added public canonical ref parse/format round-trip tests and fuzzing.
- Added archive walk/write property tests and unsafe archive path rejection.
- Added status schema error reporting instead of silently returning zero counts.
- Added direct `internal/search` tests for filters, artifacts, and FTS query escaping.
- Added Codex discovery tests to reject non-session JSONL files.
- Fixed `refresh --json` to use stable lower-case report keys via JSON tags.
- Fixed `search --json` to preserve structured `ref` and add copy-pastable `ref_text`.
- Fixed artifact discovery so disabling subagent sessions does not suppress normal-session artifacts.
- Added static test-quality guards against `t.Log` as assertion substitute and focused/sleep-based tests.
- Added ingest failure-injection hooks/tests for rollback after file blobs and cleanup after bundle promotion.
- Split ingest setup into `ingestPlan`, metadata insertion, and bundle promotion helpers.
- Added multi-store concurrent duplicate ingest coverage and busy retry for SQLite writer contention.
- Added real-ish committed parser fixtures for Pi, Claude Code, Codex, and OpenCode.
- Added CLI flag metadata/FlagSet sync coverage.
- Added resolver tests for exact-vs-prefix precedence and literal `%`/`_` handling.
- Added search path-filter wildcard escaping coverage.
- Routed `aha read <ref>` through typed `corpus.ReadRef` and added artifact search-ref round-trip coverage.

## TDD misses found

1. **Refresh JSON report shape**
   - Miss: implementation embedded `corpus.IngestReport` directly, leaking PascalCase keys.
   - Fix: failing JSON contract test first; added JSON tags.

2. **Search JSON ref round-trip**
   - Miss: `--refs` emitted copyable refs, but `--json` exposed only structured internal refs.
   - Fix: failing CLI round-trip test first; `search.Result` preserves structured `ref` and adds copy-pastable `ref_text`.

3. **Archive path policy**
   - Miss: archive validation checked sizes/SHA/duplicates but not unsafe path names.
   - Fix: failing tests for `..`, absolute paths, backslashes, and reserved names; added shared archive data-path validator.

4. **Silent status failures**
   - Miss: `corpus.Status` ignored SQL errors and could report zero counts on a corrupt/wrong schema.
   - Fix: failing schema-error test first; `Status` now returns `(map, error)`.

5. **Codex discovery semantics**
   - Miss: code comment claimed non-session JSONL would be avoided, but implementation included it.
   - Fix: failing discovery test first; now only `rollout-*.jsonl` and `history.jsonl` are accepted.

6. **IncludeSubagents flag behavior**
   - Miss: artifact capture was accidentally tied to subagent-session inclusion.
   - Fix: failing flag-matrix test first; artifact discovery now runs for retained normal sessions regardless of `IncludeSubagents`.

## Second audit results

A fresh reviewer pass after the fixes found no P0/P1 blockers after these follow-ups:

- Bundle staging now uses `os.CreateTemp` rather than timestamp-only names, removing concurrent staging collisions.
- Search path filters now escape SQL LIKE wildcards for message cwd and artifact raw paths.
- Concurrent duplicate ingest is covered with two independently opened stores and `-race -count=5` validation.
- Ingest failure tests assert full rollback across bundle/session/message/artifact/FTS tables plus staging cleanup.

## Third audit results

A later TDD pass addressed additional reviewer findings:

- Unsupported source adapters during ingest now fail explicitly instead of silently reporting success with zero parsed sessions.
- Archive writing is deterministic independent of caller file ordering; `Write` sorts a copy of manifest and captured files.
- Archive read/write/stream/walk now validate semantic manifest fields: schema, bundle ID, machine ID, capture timestamp, file source, supported kind, and source/kind path consistency.
- `search` and `read` now reject mutually exclusive output mode combinations instead of silently picking one.
- Artifact `ref_text` now formats as canonical `artifact:v1:<sha>` so parsing preserves artifact kind.

A fresh reviewer pass found no P0/P1 regressions after these changes.

## Fourth audit results

The remaining deferrable items were addressed with another red/green/refactor pass:

- Ingest is split into internal planner / blob publisher / corpus writer seams while preserving the public `IngestBundle` API and one-transaction write path.
- Held-writer visibility now covers both `search.Query` and `ReadContext`: a separate reader cannot see paused uncommitted ingest rows, then can see them after commit.
- Adapter-level fixture tests now call `Pi`, `ClaudeCode`, and `CodexCLI` wrappers directly and assert source/session identity, roles, parent links, tool metadata, image assets, model/tokens, cwd, and timestamps.
- `search` and `read` now use shared `FlagSpec` definitions for registry flags, parser defaults/help, and post-positional reordering; tests verify help/default text and every spec-backed post-positional flag.

A fresh reviewer pass found no P0/P1 regressions. Remaining lower-priority opportunities are to expand fixtures with anonymized real captures and eventually migrate the rest of the commands to `FlagSpec` as they change.

## Best-practice checks to keep

- Real temp dirs, real SQLite, real tar/zstd bundles over mocks.
- Golden files for stable human/agent output.
- JSON decoding into explicit structs for public contracts.
- Property/fuzz tests for parsers, refs, archive walking, and user query text.
- Doc/code sync tests for generated docs and README command references.
- Static test-quality guardrails for weak or logging-only tests.
