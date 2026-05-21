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
- Added a static test-quality guard against `t.Log` as assertion substitute.

## TDD misses found

1. **Refresh JSON report shape**
   - Miss: implementation embedded `corpus.IngestReport` directly, leaking PascalCase keys.
   - Fix: failing JSON contract test first; added JSON tags.

2. **Search JSON ref round-trip**
   - Miss: `--refs` emitted copyable refs, but `--json` exposed only structured internal refs.
   - Fix: failing CLI round-trip test first; `search.Result` now emits `ref` string and `hit_ref` object.

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

## Remaining audit findings

- `internal/corpus/ingest.go` still mixes bundle staging, validation, blob writes, DB inserts, indexing, conflict handling, and report construction. Consider extracting an ingest plan / blob publisher / report DTO boundary before adding failure-injection tests.
- Ingest has non-transactional filesystem side effects. Add failure-injection tests for DB errors after blob writes and after promoted bundle rename; decide whether orphan blobs are acceptable/repairable.
- Parser tests need real-ish `testdata/` fixtures for Pi, Claude Code, and Codex transcript variants; current fixtures are still simplified.
- CLI command metadata still duplicates flag definitions. Long term, define `FlagSpec` once and generate `flag.FlagSet`, help, docs, and schema metadata from it.
- Add direct resolver tests for session/entry prefix ambiguity and LIKE wildcard escaping.
- Add concurrency tests beyond `go test -race`: concurrent duplicate ingest, search/read during ingest, and simultaneous blob writes.

## Best-practice checks to keep

- Real temp dirs, real SQLite, real tar/zstd bundles over mocks.
- Golden files for stable human/agent output.
- JSON decoding into explicit structs for public contracts.
- Property/fuzz tests for parsers, refs, archive walking, and user query text.
- Doc/code sync tests for generated docs and README command references.
- Static test-quality guardrails for weak or logging-only tests.
