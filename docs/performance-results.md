# Performance results

Date: 2026-05-25
Branch: `claude/correctness-by-construction-cgcFx`
Baseline branch point: pre-performance-plan notes in `docs/performance-scalability-plan.md`
Machine: Apple M2 Ultra, darwin/arm64

These numbers are directional benchmark captures, not CI thresholds. Unit tests guard algorithmic hazards with query plans, property tests, and fake-driver operation counters; benchmarks show magnitude and regressions.

## Validation commands

```bash
go test ./...
scripts/verify.sh full
```

Latest targeted benchmark commands:

```bash
go test ./internal/corpus -run=^$ -bench='BenchmarkPathologicalIngestManyTinyEntries/entries_10000$|BenchmarkPathologicalVerifyFTSJoinScaling/messages_5000$|BenchmarkPathologicalStatusAndBundleSHAs' -benchtime=1x -benchmem
go test ./internal/corpus -run=^$ -bench='BenchmarkPathologicalIngestManyTinyEntries/entries_50000$' -benchtime=1x -benchmem
go test ./internal/corpus -run=^$ -bench='BenchmarkPathologicalIngestManyTinyEntries/entries_100000$' -benchtime=1x -benchmem
go test ./internal/search -run=^$ -bench='BenchmarkPathologicalQueryBroadTermPathFilter' -benchtime=1x -benchmem
go test ./internal/search -run=^$ -bench='BenchmarkQuery' -benchtime=10x -benchmem
go test ./internal/depot -run=^$ -bench='BenchmarkPathologicalCatalogMergeManyTrivialRefs|BenchmarkPathologicalLocalDepotLargeCatalog' -benchtime=1x -benchmem
```

## Summary metrics

| Area | Before / risk signal | Latest result | Outcome |
|---|---:|---:|---|
| Corpus verify, 5k messages | ~`7.68s`, superlinear FTS key joins | `16.2ms`, `7.5KB`, `188 allocs` | Fixed algorithmic cliff; still near-linear with rowid-backed FTS checks. |
| Ingest, 10k tiny entries | `1.23s`, `136MB`, `1.19M allocs` | `1.12s`, `84.1MB`, `728k allocs` | Lower memory/allocs and modest time improvement. |
| Ingest, 50k tiny entries | not previously captured | `5.51s`, `464MB`, `3.65M allocs` | Roughly linear scaling from 10k. |
| Ingest, 100k tiny entries | not previously captured | `11.2s`, `756MB`, `7.30M allocs` | Roughly linear scaling from 50k; confirms no obvious O(n²) cliff. |
| Search broad term, requested limit 1000 | `51–57ms`, `1.78MB`, `33k allocs` | `50.6ms`, `344KB`, `6.7k allocs` | Runtime still FTS-candidate bound; output/allocation cost is capped. |
| Search path contains rare match | `40–42ms` | `40.5ms`, `42.8KB`, `731 allocs` | Similar; contains remains convenience slow path. |
| Search path-token rare match | new indexed filter | `42.7ms`, `43.1KB`, `729 allocs` | Indexed semantics/cost guardrail; broad FTS dominates this synthetic query. |
| Search project rare match | new indexed filter | `44.4ms`, `43.0KB`, `729 allocs` | Indexed semantics/cost guardrail; broad FTS dominates this synthetic query. |
| Search normal bench, path-token filter | new | `7.17ms`, `38.6KB`, `730 allocs` over `10x` | Comparable to exact project/path filters on small corpora. |
| Status counts | ~`1.9ms` | `2.0ms`, `9.8KB`, `260 allocs` | Slightly worse in this run due extra path-token table counts; still metadata-only and cheap. |
| Bundle SHA set | `1.8ms`, `1.25MB` | `1.66ms`, `1.25MB`, `25k allocs` | Similar; still grows with ingested bundle count. |
| Depot catalog merge, 4k refs/1k unique | linear-scan duplicate risk | `1.51ms`, `1.49MB`, `9.8k allocs` | Map-backed dedupe removes bad duplicate scaling; small memory cost accepted. |
| Local depot list, 250 refs | ~`0.7ms` | `0.95ms`, `374KB`, `2k allocs` | Slightly slower but still metadata-only. |
| Local depot append, growing catalog | ~`2.0ms` | `2.25ms`, `880KB`, `2.2k allocs` | Slightly slower; catalog JSON rewrite remains per-machine. |
| Local depot deep verify, 250 refs | ~`24.9ms`, `20MB` | `28.2ms`, `21.6MB`, `34k allocs` | Slightly worse; deep verify is intentionally byte-linear and explicit. |

## Correctness/performance guardrails added

- Query-plan tests for rowid-backed FTS verification and actual search SQL using indexed project/path-token filters.
- Fake-R2 operation-budget tests for quick verify, deep verify, repair, list, and compact.
- Property tests for depot behind counts and map-backed catalog merge semantics.
- Contract tests for duplicate bundle parse skipping and known file-blob no-recompression.
- CLI/status/verify counters for depot refs/fetches, depot bytes read/downloaded, verify row counts, and FTS repair rows.
- Pathological benchmarks for ingest/search/status/verify/depot plus optional 50k/100k ingest scales.

## What got worse

- `corpus.Verify` allocs rose from about `119` to `188` after adding user-facing row-count stats. The runtime remains ~milliseconds and the former seconds-scale cliff is gone.
- Ingest is faster than the first baseline but slower than an intermediate best run (~`1.01s`) because path-token maintenance adds derived index writes. The accepted tradeoff buys indexed path filters.
- Status counts are slightly slower because it now counts path-token tables too.
- Depot catalog merge/list/deep-verify constants are slightly worse than the first rough run because map-backed dedupe and byte counters preserve more metadata. Deep verify remains explicit.
- Broad-term search time did not materially improve; SQLite FTS candidate ranking dominates. The plan improved bounded output/cost, not broad-term ranking latency.

## Finished vs deferred

Finished for v1:

- remove redundant hot-path hash/read passes where the writer already knows bundle identity;
- keep no-op refresh and status metadata-only when state/catalog metadata is present;
- validate expected depot SHA during ingest staging instead of pre-hashing separately;
- rowid-backed FTS verification;
- prepared-statement ingest and per-session duplicate/conflict prefetch;
- indexed `--project` and `--path-token` filters, with `--path` documented as contains;
- high-limit search cap and warning;
- quick/deep depot verify split with operation-budget tests and byte counters;
- corpus size/vacuum/prune-orphans and depot compact;
- captured benchmark suite and validation commands.

Deferred because they need real-world evidence or product policy:

- true multi-row ingest inserts: current scaling is linear; add only if profiles show SQLite step overhead dominates;
- depot summary/status cache: add only if raw catalog scans become a real bottleneck after compaction;
- retention/export/delete policies beyond orphan pruning: requires explicit product/trust decisions;
- large real-corpus and real-R2 validation: needs private data/credentials and should be run before release announcements.
