# Performance audit

Date: 2026-05-19

Scope: snapshot/archive, ingest, search, depot, status, verify, and refresh flows in the current Go/SQLite implementation.

## Summary

`aha` optimizes first for determinism, integrity, and repairability. That is the right tradeoff for a history archive, but it creates several predictable cost centers: repeated hashing, repeated archive reads, serial ingest SQL, full-scan verification, and depot operations that scale with catalog/bundle count.

An initial benchmark suite now covers archive write/read, corpus ingest/verify/reconcile, search, and local depot put/list/verify. Pathological benchmarks, property-based performance invariants for many trivial bundles, cheapest-layer audit findings, and pprof notes are summarized in `docs/performance-scalability-plan.md`. The risks below are still directional until we add larger fixtures and CI trend capture.

## Algorithmic complexity

Variables:

- `F`: manifest files in a bundle;
- `B`: total uncompressed source bytes;
- `Z`: compressed bundle bytes;
- `E`: parsed entries;
- `M`: indexed message rows;
- `A`: artifact rows;
- `D`: depot bundle refs/objects;
- `C`: catalog shards;
- `L`: requested search limit.

| Operation | Time | Extra space | Notes |
|---|---:|---:|---|
| `snapshot` capture | `O(F log F + B)` | `O(max file)` plus temp copies | Sorting gives determinism; stable copy and parsing scan source bytes. |
| archive write | `O(F log F + B + Z)` | `O(max file)` | Sorts manifest/files, streams tar/zstd, then currently hashes final bundle. |
| local depot put | `O(Z + D_m log D_m)` | `O(Z)` during copy buffers / catalog JSON | Current path derives bundle ref by reading/hash-validating the bundle and rewrites one machine catalog shard of size `D_m`. |
| R2 depot put | `O(Z + D_m log D_m)` plus network | `O(D_m)` catalog JSON | Upload bytes dominate for large bundles; catalog conditional write retries can repeat shard merge. |
| unchanged `refresh` check | `O(C)` for refs with `state_sha256`; legacy/missing state fallback `O(C + K*Z_old)` | `O(max old bundle)` only on fallback | New catalog refs compare state metadata without fetching old bundles. |
| ingest bundle | `O(Z + B + E*q_entry + assets)` | `O(max file)` temp plus SQLite rows | `q_entry` is per-entry lookup/conflict SQL cost; indexes keep it near logarithmic but the loop is chatty. |
| duplicate ingest | `O(Z)` currently | `O(1)` plus staging | Bundle is copied/hashed before duplicate detection completes. |
| search | `O(FTS(query) + L log L)` | `O(L)` | Message/artifact FTS queries each cap at `L`, then Go merges/sorts. Path filters can force more scanning because `LIKE '%x%'` is not indexable. |
| status | `O(number of tables + D if --depot)` | `O(D)` for depot refs | Local status is count queries; `--depot` can perform network catalog listing. |
| corpus verify | `O(M + A + FTS + bundles)` | `O(problems)` | Full left-join counts and bundle blob stats. |
| `verify --repair-fts` | `O(M + A + FTS)` | SQLite FTS rebuild space | Deletes/reinserts derived FTS rows. |
| depot verify | quick/default `O(C + D metadata)`; `--deep` `O(D*Z)` local/R2 | quick `O(D refs)`; deep `O(max bundle)` temp | Quick checks metadata/existence; deep integrity rehashes/downloads every object. |

## Likely hotspots

| Area | Evidence | Risk | Improvement |
|---|---|---|---|
| Snapshot/archive repeated passes | `archive.Capture` still stable-copies and hashes source files; `archive.WriteWithInfo` now computes compressed SHA while writing and depot publish can use known identity. | Source-file passes remain; finished-bundle rehashes are reduced on snapshot/refresh publish. | Add byte counters to guard against regression and continue collapsing safe passes. |
| Refresh unchanged check | New catalog refs include `state_sha256`; `findDepotBundleWithSameState` uses it before fetching old bundles. | Refs missing state metadata still use fallback; broad duplicate/machine orderings need more PBT. | Add wider PBT/fake-R2 operation-count tests. |
| Depot ingest pre-hash | `ingestFromDepot` now passes expected SHA to ingest staging instead of hashing separately. | Staging still copies/hashes once, which is the integrity boundary. | Add byte counters and benchmark large pending bundles. |
| Ingest entry materialization | Archive walker hashes each entry; ingest spools/hashes each entry; file blob publishing re-reads temp files to zstd-compress. | Large session/artifact files are read more than once. | Collapse spool/hash/blob compression where safe; skip compression rewrite when content-addressed blob already exists. |
| Per-entry SQL chatter | Each parsed entry performs lookup/conflict checks/inserts and optional asset/image writes. | Many small entries can be dominated by prepare/step overhead. | Prepare statements per transaction; prefetch existing hashes/conflict candidates per session. |
| FTS verify/reconcile | `Verify` runs full left-join counts; `ReconcileFTS` deletes/rebuilds all FTS rows. | Expensive on large corpora; `--repair-fts` is intentionally a maintenance operation. | Keep default verify read-only; consider scoped/incremental repair later. |
| Search path filters | Path filters use `LIKE '%...%'`; message and artifact results are queried separately then merged/sorted in Go. | Non-indexable path filters and high limits can be slow. | Cap/validate `--limit`; add benchmarks for default, filtered, no-hit, and high-limit searches. |
| Depot verify | Quick/default verify checks metadata/object existence; `--deep` rehashes every local bundle or downloads every R2 bundle. | Correct deep verification remains expensive and network-heavy. | Expand operation-budget tests and progress/counter reporting. |
| Status with depot | `status --depot` lists catalog refs; R2 listing performs network calls. | A nominal status command can become remote/costly when `--depot` is set. | Document this; add operation-count tests with fake R2. |

## Benchmark and profiling plan

Run the initial and pathological benchmarks before optimizing:

```bash
go test ./internal/archive ./internal/corpus ./internal/search ./internal/depot -run=^$ -bench=. -benchmem
go test ./internal/archive ./internal/corpus ./internal/search ./internal/depot -run=^$ -bench=BenchmarkPathological -benchmem
```

For command-level profiles, opt in with either flags or environment variables:

```bash
aha --cpuprofile cpu.pprof --memprofile heap.pprof verify --repo ~/.aha/corpus
AHA_CPU_PROFILE=cpu.pprof AHA_MEM_PROFILE=heap.pprof aha refresh
```

Inspect with:

```bash
go tool pprof -http=:0 cpu.pprof
go tool pprof -http=:0 heap.pprof
```

Suggested scenarios:

- Archive: `1000x4KiB`, `100x1MiB`, `5x100MiB` files; compare `Capture`, `Write`, `ReadManifest`, `StreamManifestFiles`.
- Ingest: many sessions/entries/messages/artifacts; first ingest, duplicate ingest, local-depot ingest.
- Search: 100k+ messages plus artifacts; default query, no-hit query, source/role filters, path filter, high limit.
- Depot: local depot with 100/1k/10k catalog refs; fake-R2 operation counts for list/put/verify.
- Verify/status: large corpus; `Status`, `Verify`, `ReconcileFTS`, `status --depot`.

## Initial benchmark observations

A one-shot smoke benchmark (`go test ./internal/archive ./internal/corpus ./internal/search ./internal/depot -run=^$ -bench=. -benchtime=1x -benchmem`) on an Apple M2 Ultra showed the expected hotspots. These numbers are not stable performance targets, but they are useful directionally:

- `BenchmarkIngestBundle` over a 10-session/1000-entry synthetic bundle took about `120ms` and allocated about `59MB`.
- Before rowid-based FTS verification, `BenchmarkVerify` over a 20-session/2000-entry synthetic corpus took about `1.0s`, and the pathological 5k-message verify took about `7.7s`; after the rowid fix, the 5k pathological benchmark is about `15ms`.
- `BenchmarkReconcileFTS` over the same corpus took about `15ms`.
- Default FTS search over the 2000-entry corpus took about `9ms`; no-hit search was much cheaper.
- Local depot list/verify over small seeded depots was sub-millisecond to a few milliseconds, but complexity remains linear in catalog refs/bundle bytes.

The first optimization target, corpus verify query shape/indexing, has been implemented. The next targets are ingest SQL chatter, indexed project/path filters, and operation-budget coverage for depot/status paths.

## Guardrails for optimization

Do not trade away these invariants for speed:

- deterministic archive bytes;
- manifest-listed SHA/size validation;
- path/symlink safety;
- append-only/conflict quarantine behavior;
- content-addressed bundle/object identity;
- raw data preservation;
- repairability of derived FTS state.

Performance work should preserve a deep/strict verification path even when adding fast paths.
