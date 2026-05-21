# Testing and abstraction audit

Date: 2026-05-21

Inputs:

- `github.com/adewale/testing-best-practices` installed locally as a Pi skill and reviewed.
- Existing adewale Go repo guidance reviewed from `rogue_planet` and `olsen`, especially assertion-quality audits, doc/code consistency audits, real-object tests, and boundary-condition lessons.
- Fresh codebase review by scout/reviewer subagents.

## Changes made from the audit

- Added machine-readable CLI error contracts and regression tests for flag parse errors.
- Added generated `docs/commands.md` from command metadata and a sync test.
- Added golden tests for human/JSON/refs/files/Markdown rendering.
- Added public HitRef parse/format round-trip tests and fuzzing.
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
- Added real-ish committed parser fixtures for Pi, Claude Code, and Codex.
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

## Remaining deferrable findings

- `internal/corpus/ingest.go` is improved but still owns parsing, blob writes, DB writes, conflict detection, FTS indexing, and report construction. A deeper split into planner / blob publisher / DB writer can wait until the next substantial ingest feature.
- Add a held-writer/barrier test for search/read visibility during an open ingest transaction. Current tests cover duplicate ingest contention and rollback, but not reader consistency under a deliberately paused writer.
- Parser fixtures are real-ish and committed, but still synthetic. Expand them with captured anonymized examples when available.
- CLI flag sync now catches name drift, but defaults/help text/reorder maps are still separately maintained.
- Add more adapter-level fixture tests that call `PiCLI`, `ClaudeCode`, and `CodexCLI` wrappers, not only `parseGenericJSONL`.

## Best-practice checks to keep

- Real temp dirs, real SQLite, real tar/zstd bundles over mocks.
- Golden files for stable human/agent output.
- JSON decoding into explicit structs for public contracts.
- Property/fuzz tests for parsers, refs, archive walking, and user query text.
- Doc/code sync tests for generated docs and README command references.
- Static test-quality guardrails for weak or logging-only tests.
