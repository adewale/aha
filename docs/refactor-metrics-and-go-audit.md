# Refactor metrics and Go best-practices audit

Date: 2026-05-25
Branch: `claude/correctness-by-construction-cgcFx`
Before commit: `9584b34`
Machine: Apple M2 Ultra, darwin/arm64

*Historical capture: predates depot v2 ([docs/depot-v2-spec.md](depot-v2-spec.md)). The depot local/R2 and catalogue-merge code measured here is v1 machinery that was replaced by content-addressed snapshots in June 2026.*

Scope: duplication-refactor candidates from `docs/audits/code-duplication-audit.md`: depot local/R2 shared helpers, R2 marker helpers, snapshot/refresh flag registration, search predicate construction, corpus FTS predicates, and atomic file-write helpers.

Metrics are directional. The stable table uses repeated package benchmarks without pprof. The profile table uses one-shot benchmark runs with `-cpuprofile`/`-memprofile` to identify hotspots, not to gate wall-clock time.

## Artefacts

Not committed; retained under `/tmp` on the capture machine:

- Before one-shot profiles: `/tmp/aha-refactor-metrics-before/`
- Before stable repeated benches: `/tmp/aha-refactor-metrics-before-stable/`
- After one-shot profiles: `/tmp/aha-refactor-metrics-after-final2/`
- After stable repeated benches: `/tmp/aha-refactor-metrics-after-stable-final/`
- Extra catalogue-merge check: `/tmp/aha-refactor-catalog-merge-extra.txt`

## Commands

```bash
# before, from a detached worktree at 9584b34
go test ./internal/corpus -count=3 -run=^$ -bench='BenchmarkPathologicalIngestManyTinyEntries/entries_10000$|BenchmarkPathologicalVerifyFTSJoinScaling/messages_5000$' -benchtime=1x -benchmem
go test ./internal/search -count=3 -run=^$ -bench='BenchmarkPathologicalQueryBroadTermPathFilter/broad-term-limit-1000$' -benchtime=10x -benchmem
go test ./internal/depot -count=3 -run=^$ -bench='BenchmarkPathologicalCatalogMergeManyTrivialRefs|BenchmarkPathologicalLocalDepotLargeCatalog' -benchtime=1x -benchmem

# after, from the working tree after refactors
go test ./internal/corpus -count=3 -run=^$ -bench='BenchmarkPathologicalIngestManyTinyEntries/entries_10000$|BenchmarkPathologicalVerifyFTSJoinScaling/messages_5000$' -benchtime=1x -benchmem
go test ./internal/search -count=3 -run=^$ -bench='BenchmarkPathologicalQueryBroadTermPathFilter/broad-term-limit-1000$' -benchtime=10x -benchmem
go test ./internal/depot -count=3 -run=^$ -bench='BenchmarkPathologicalCatalogMergeManyTrivialRefs|BenchmarkPathologicalLocalDepotLargeCatalog' -benchtime=1x -benchmem

# profiles, before and after, same benchmark patterns with -cpuprofile/-memprofile
```

## Stable before/after metrics

Median of 3 runs unless noted. Catalogue merge also got a `-benchtime=5x -count=5` check because the `1x` run was noisy.

| Benchmark | Before | After | Regression check |
|---|---:|---:|---|
| Ingest 10k tiny entries | `1.13s`, `84.1MB`, `728k allocs` | `1.07s`, `86.5MB`, `728k allocs` | No time/alloc regression; memory within observed pre-refactor variance. |
| Verify 5k messages | `16.4ms`, `7.5KB`, `188 allocs` | `15.2ms`, `7.5KB`, `188 allocs` | No regression. |
| Search broad term, requested limit 1000 | `54.7ms`, `341KB`, `6663 allocs` | `49.7ms`, `341KB`, `6662 allocs` | No regression. |
| Catalogue merge, 4k refs/1k unique (`5x`, median of 5) | `837µs`, `705KB`, `2771 allocs` | `792µs`, `705KB`, `2771 allocs` | No regression; `1x` runs were noisy. |
| Local depot list, 250 refs | `1.15ms`, `374KB`, `2042 allocs` | `0.91ms`, `374KB`, `2042 allocs` | No regression. |
| Local depot append growing catalogue | `2.57ms`, `~1.11MB`, `~2246 allocs` | `2.40ms`, `~1.12MB`, `~2268 allocs` | No material regression; tiny alloc increase buys no-replace publish semantics. |
| Local depot deep verify, 250 refs | `34.4ms`, `21.7MB`, `34.3k allocs` | `31.8ms`, `21.4MB`, `34.1k allocs` | No regression. |

## Profile summary

After-refactor one-shot profile outputs:

| Target | Output | Profile finding |
|---|---:|---|
| Ingest 10k tiny entries | `1.09s/op`, `84.2MB/op`, `728k allocs/op` | Still dominated by SQLite/syscalls; no new Go helper appears as a hot frame. |
| Verify 5k messages | `15.3ms/op`, `7.5KB/op`, `188 allocs/op` | Still small SQLite count work; shared FTS predicates did not reintroduce allocation regression. |
| Search broad term | `49.6ms/op`, `340.8KB/op`, `6662 allocs/op` | Still SQLite/FTS candidate bound; predicate-builder refactor did not change the profile shape. |
| Depot local verify/list/append | same order as before | Still syscall/JSON/catalogue dominated; atomic helper is not a visible hot frame. |

## Refactor changes completed

- Shared depot known-bundle normalization via `prepareKnownBundleRef`.
- Shared quick catalogue-ref verification via `verifyCatalogRefs` with backend-specific existence checks.
- Shared catalogue shard grouping/merge/write helpers for compact and repair paths.
- R2 marker read/write JSON helpers and non-404 marker error classification.
- Shared snapshot/refresh flag registration via `snapshotFlagSet`.
- Shared search filter predicate construction for message/artefact SQL.
- Shared corpus FTS text predicates between verify, repair, and artefact triggers.
- Shared atomic file-write/copy helpers in `internal/fileutil`, including no-replace `ExistingOK` semantics and created/not-created reporting.

## Regression verification

Passed after the refactors and follow-up audit fixes:

```bash
scripts/verify.sh full
```

This includes `go test ./...`, whitespace checks, `go vet ./...`, `go test -race ./...`, bounded fuzz targets, and `go build -o /tmp/aha ./cmd/aha`.

## Go best-practices audit

A fresh reviewer audit after validation found no fix-before-commit findings.

Issues found and fixed during the audit:

- Artefact FTS trigger now uses the same empty-body preview fallback as `verify` and `repair`; migration v7 recreates the trigger and rebuilds artefact FTS for existing corpora.
- Atomic `ExistingOK` writes now use no-replace hard-link publish semantics instead of stat-then-rename overwrite risk, and local depot `created` reflects whether this process actually published the object.
- R2 marker reads now classify only S3 not-found codes as missing; auth/network/context errors propagate instead of causing marker recreation attempts.

Remaining note: benchmark wall-clock results are not CI thresholds; correctness and cost regressions are guarded by unit/property/query-plan/fake-driver tests plus the full verification profile above.
