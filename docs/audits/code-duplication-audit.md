# Code duplication audit

Date: 2026-05-25

Scope: production Go code under `cmd/aha` and `internal/*`. Test fixtures and generated docs were excluded unless duplication creates maintenance risk.

## Result

No blocker-level duplication was found. The codebase already has useful shared seams for common CLI/corpus behavior (`registerCorpusFlags`, `openCorpusForCommand`, `writeJSON`, typed refs, archive/depot/corpus APIs). The remaining duplication is mostly backend parity and flag/SQL construction. The risky items are now tracked below and in `TODO.md`.

## Findings

| Priority | Area | Duplicated pattern | Risk | Recommendation |
|---|---|---|---|---|
| Fix before next depot/R2 change | `internal/depot/local.go`, `internal/depot/r2.go` | `PutBundleKnown` normalizes known bundle refs in both backends: fill key, fill filename, validate, stat size. | Local and R2 could drift on the known-bundle metadata contract. | Extract a pure helper such as `prepareKnownBundleRef(bundlePath, ref)` and keep backend-specific storage separate. |
| Fix before next depot/R2 change | `internal/depot/local.go`, `internal/depot/r2.go` | Quick catalog verification loops duplicate SHA/key/default-key validation before backend-specific existence checks. | Catalog integrity fixes must be applied twice; this is core repair/verify behavior. | Share a catalog-ref validation loop with an `exists(key)` callback. |
| Fix before next depot/R2 compact or repair change | `internal/depot/local.go`, `internal/depot/r2.go` | Catalog compaction and repair rebuild paths repeat machine normalization, `MergeBundleRefs`, shard write, and report accounting with backend-specific enumerate/write plumbing. | Compact/repair semantics can drift between local and R2, especially around duplicate preservation, machine attribution, and report counts. | Extract a catalog-shard merge/rewrite helper parameterized by backend list/read/write callbacks. |
| Fix when touching R2 verify/init | `internal/depot/r2.go` | Marker get/decode/validate and marker JSON write patterns are repeated across init/verify paths. | Error classification and marker semantics can drift between `init`, quick verify, and deep/repair flows. | Extract `getR2Marker`, `putR2JSON`, or `ensureR2Marker`. |
| Fix before adding snapshot flags | `internal/cli/command_snapshot.go` | Snapshot and refresh register the same snapshot-related flags separately. | New snapshot options can be added to `snapshot` but forgotten for `refresh`. | Extract shared snapshot flag registration that returns a request/options struct; let `refresh` add only corpus/repo behavior. |
| Defer until search filters change | `internal/search/search.go` | Message and artifact SQL builders repeat source, machine, time, path, path-token, and project predicate construction. | New filters can diverge between message and artifact search semantics. | Introduce a small predicate builder parameterized by aliases/columns when search filtering changes next. |
| Defer until FTS/indexing changes | `internal/corpus/verify.go`, `internal/corpus/fts_reconcile.go` | Verify and FTS repair repeat the same “indexable text” predicates for messages/artifacts. | Drift could make `verify --repair-fts` fail to repair exactly the rows `verify` reports missing. | Share a tiny predicate/query helper before changing FTS indexing or repair scope. |
| Defer until storage paths change | `internal/depot/depot.go`, `internal/corpus/ingest.go` | Atomic temp create/copy/close/rename/cleanup flows recur for depot bundles and corpus blob writes. | Future durability changes such as fsync or race handling could be applied inconsistently. | Add `internal/fileutil` atomic copy/write helpers when blob/depot write paths are touched. |

## Non-findings

- Search/read/output-mode helpers are intentionally shared enough for current scope; no broad CLI framework rewrite is justified.
- Adapter parsers keep source-specific behavior isolated; superficially similar parsing code should not be merged until real fixtures prove common semantics.
- Tests intentionally repeat some setup to make scenarios explicit; no test-fixture consolidation is required for v1.

## Validation

This audit is review-only. The current follow-up is documentation and TODO tracking, not refactoring. Refactors should be paired with existing contract/property tests plus focused regression tests for local/R2 parity or snapshot/refresh flag parity.
