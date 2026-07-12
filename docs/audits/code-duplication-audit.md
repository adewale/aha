# Code duplication audit

Date: 2026-05-25

*Historical: predates depot v2 ([../depot-v2-spec.md](../depot-v2-spec.md)). Findings that reference bundle/catalogue depot code (`PutBundleKnown`, catalogue verify/compact/repair) describe v1 machinery that was replaced by content-addressed snapshots in June 2026.*

Scope: production Go code under `cmd/aha` and `internal/*`. Test fixtures and generated docs were excluded unless duplication creates maintenance risk.

## Result

No blocker-level duplication was found. The codebase already has useful shared seams for common CLI/corpus behaviour (`registerCorpusFlags`, `openCorpusForCommand`, `writeJSON`, typed refs, archive/depot/corpus APIs). The risky items identified below were implemented in the follow-up duplication-refactor pass; before/after metrics and the Go best-practices audit are captured in `docs/refactor-metrics-and-go-audit.md`.

## Findings and follow-up status

| Priority | Area | Duplicated pattern | Risk | Recommendation / status |
|---|---|---|---|---|
| Fixed | `internal/depot/local.go`, `internal/depot/r2.go` | `PutBundleKnown` normalizes known bundle refs in both backends: fill key, fill filename, validate, stat size. | Local and R2 could drift on the known-bundle metadata contract. | Added shared `prepareKnownBundleRef`. |
| Fixed | `internal/depot/local.go`, `internal/depot/r2.go` | Quick catalogue verification loops duplicate SHA/key/default-key validation before backend-specific existence checks. | Catalogue integrity fixes must be applied twice; this is core repair/verify behaviour. | Added shared `verifyCatalogRefs` with backend-specific existence callbacks. |
| Fixed | `internal/depot/local.go`, `internal/depot/r2.go` | Catalogue compaction and repair rebuild paths repeat machine normalization, `MergeBundleRefs`, shard write, and report accounting with backend-specific enumerate/write plumbing. | Compact/repair semantics can drift between local and R2, especially around duplicate preservation, machine attribution, and report counts. | Added shared shard grouping/merge/write helpers. |
| Fixed | `internal/depot/r2.go` | Marker get/decode/validate and marker JSON write patterns are repeated across init/verify paths. | Error classification and marker semantics can drift between `init`, quick verify, and deep/repair flows. | Added R2 marker/JSON helpers and non-404 error propagation. |
| Fixed | `internal/cli/command_snapshot.go` | Snapshot and refresh register the same snapshot-related flags separately. | New snapshot options can be added to `snapshot` but forgotten for `refresh`. | Added shared `snapshotFlagSet` registration/building. |
| Fixed | `internal/search/search.go` | Message and artefact SQL builders repeat source, machine, time, path, path-token, and project predicate construction. | New filters can diverge between message and artefact search semantics. | Added shared filter predicate builder. |
| Fixed | `internal/corpus/verify.go`, `internal/corpus/fts_reconcile.go`, `internal/corpus/schema.go` | Verify, FTS repair, and artefact triggers repeated “indexable text” predicates. | Drift could make `verify --repair-fts` fail to repair exactly the rows `verify` reports missing. | Added shared FTS predicate/expression helpers plus migration v7 for existing artefact triggers. |
| Fixed | `internal/depot/depot.go`, `internal/corpus/ingest.go`, `internal/fileutil/hashcopy.go` | Atomic temp create/copy/close/rename/cleanup flows recur for depot bundles and corpus blob writes. | Future durability changes such as fsync or race handling could be applied inconsistently. | Added shared `fileutil.Atomic*` helpers with `ExistingOK` no-replace semantics. |

## Non-findings

- Search/read/output-mode helpers are intentionally shared enough for current scope; no broad CLI framework rewrite is justified.
- Adapter parsers keep source-specific behaviour isolated; superficially similar parsing code should not be merged until real fixtures prove common semantics.
- Tests intentionally repeat some setup to make scenarios explicit; no test-fixture consolidation is required for v1.

## Validation

This audit is review-only. The current follow-up is documentation and TODO tracking, not refactoring. Refactors should be paired with existing contract/property tests plus focused regression tests for local/R2 parity or snapshot/refresh flag parity.
