# Performance audit

Original audit date: 2026-05-19
Updated: 2026-05-25

*Historical: predates depot v2 ([docs/depot-v2-spec.md](depot-v2-spec.md)). The bundle/catalogue depot mechanics analysed here were replaced by content-addressed snapshots in June 2026; depot-related complexity rows and risks below describe the v1 design.*

Scope: snapshot/archive, ingest, search, depot, status, verify, and refresh flows in the current Go/SQLite implementation.

## Summary

`aha` optimises first for determinism, integrity, and repairability. That is the right tradeoff for a history archive, but it creates several predictable cost centres: repeated hashing, repeated archive reads, serial ingest SQL, full-scan verification, and depot operations that scale with catalogue/bundle count.

An initial benchmark suite now covers archive write/read, corpus ingest/verify/reconcile, search, and local depot put/list/verify. Pathological benchmarks, property-based performance invariants for many trivial bundles, cheapest-layer audit findings, and pprof notes are summarized in `docs/performance-scalability-plan.md`. The risks below are still directional until we add larger fixtures and CI trend capture.

## Algorithmic complexity

Variables:

- `F`: manifest files in a bundle;
- `B`: total uncompressed source bytes;
- `Z`: compressed bundle bytes;
- `E`: parsed entries;
- `M`: indexed message rows;
- `A`: artefact rows;
- `D`: depot bundle refs/objects;
- `C`: catalogue shards;
- `L`: requested search limit.

| Operation | Time | Extra space | Notes |
|---|---:|---:|---|
| `snapshot` capture | `O(F log F + B)` | `O(max file)` plus temp copies | Sorting gives determinism; stable copy and parsing scan source bytes. |
| archive write | `O(F log F + B + Z)` | `O(max file)` | Sorts manifest/files and streams tar/zstd; `WriteWithInfo` computes compressed SHA/size during the write. |
| local depot put | `O(Z + D_m log D_m)` | `O(Z)` during copy buffers / catalogue JSON | Known bundle refs avoid rehashing after snapshot; one machine catalogue shard is rewritten. |
| R2 depot put | `O(Z + D_m log D_m)` plus network | `O(D_m)` catalogue JSON | Upload bytes dominate for large bundles; catalogue conditional write retries can repeat shard merge. |
| unchanged `refresh` check | `O(C)` for refs with `state_sha256`; legacy/missing state fallback `O(C + K*Z_old)` | `O(max old bundle)` only on fallback | New catalogue refs compare state metadata without fetching old bundles. |
| ingest bundle | `O(Z + B + E + assets + path_tokens)` | `O(max file + entries_in_session)` temp plus SQLite rows | Prepared statements and per-session prefetch remove per-entry duplicate/conflict queries; inserts remain linear. Known file blobs skip recompression. |
| duplicate ingest | `O(Z)` currently | `O(1)` plus staging | Bundle is copied/hashed before duplicate detection completes. |
| search | `O(FTS(query) + min(L,200) log min(L,200))` | `O(min(L,200))` | Message/artefact FTS queries each cap at `L<=200`, then Go merges/sorts. Exact `--project` and `--path-token` are indexed; `--path` remains non-indexable contains. |
| status | `O(number of tables + D if --archive)` | `O(D)` for depot refs | Local status is count queries; `--archive` can perform network catalogue listing. |
| corpus verify | `O(M + A + FTS + bundles)` | `O(problems)` | Full left-join counts and bundle blob stats. |
| `verify --repair-fts` | `O(M + A + FTS)` | SQLite FTS rebuild space | Deletes/reinserts derived FTS rows. |
| depot verify | quick/default `O(C + D metadata)`; `--deep` `O(D*Z)` local/R2 | quick `O(D refs)`; deep `O(max bundle)` temp | Quick checks metadata/existence; deep integrity rehashes/downloads every object. |

## Likely hotspots

| Area | Evidence | Risk | Improvement |
|---|---|---|---|
| Snapshot/archive repeated passes | `archive.Capture` still stable-copies and hashes source files; `archive.WriteWithInfo` computes compressed SHA while writing and depot publish uses known identity. | Source-file passes remain; finished-bundle rehashes are reduced on snapshot/refresh publish. | Byte-counter tests guard depot/status paths; add more counters only if regression risk grows. |
| Refresh unchanged check | New catalogue refs include `state_sha256`; `findDepotBundleWithSameState` uses it before fetching old bundles. | Refs missing state metadata still use fallback. | Operation-count tests prove matching state metadata fetches zero old bundle bytes. |
| Depot ingest pre-hash | `ingestFromDepot` now passes expected SHA to ingest staging instead of hashing separately and emits per-bundle bytes/fetched counters for depot ingest items. | Staging still copies/hashes once, which is the integrity boundary. | Benchmark large pending bundles with real object sizes if this path becomes visible. |
| Ingest entry materialization | Archive walker hashes each entry; ingest spools/hashes each entry; file blob publishing re-reads temp files only when a content-addressed blob is not already present. | Large new session/artefact files are still read more than once at the integrity boundary. | Streaming parser is a future adapter-level optimisation, not required for current correctness/perf claims. |
| Per-entry SQL chatter | Prepared statements cover ingest writes; same-session and cross-machine conflict state are prefetched per session. | Inserts remain one row at a time so SQLite step cost is still linear. | Multi-row insert batching is optional future constant-factor work. |
| FTS verify/reconcile | `Verify` uses rowid-backed FTS identity and emits row-count stats; `ReconcileFTS` deletes/rebuilds all FTS rows and reports deleted/inserted FTS rows. | Default verify is now near-linear; `--repair-fts` remains maintenance. | Keep query-plan guard; consider scoped repair only after evidence. |
| Search path filters | `--project` and `--path-token` use indexes; `--path` uses `LIKE '%...%'`. | Contains filters and broad/common terms can still be slow. | Prefer indexed filters in docs/agent workflows; keep contains for convenience. |
| Depot verify | Quick/default verify checks metadata/object existence; `--deep` rehashes every local bundle or downloads every R2 bundle and reports bytes. | Correct deep verification remains expensive and network-heavy. | Operation-budget tests cover quick/deep/repair/list/compact. |
| Status with depot | `status --archive` lists catalogue refs and reports listed/unique refs plus zero fetches. | R2 listing performs network calls when explicitly requested. | Keep default status corpus-only; add summaries only if catalogue scans become a real bottleneck. |

## Benchmark and profiling plan

Run the initial and pathological benchmarks before optimising:

```bash
go test ./internal/archive ./internal/corpus ./internal/search ./internal/depot -run=^$ -bench=. -benchmem
go test ./internal/archive ./internal/corpus ./internal/search ./internal/depot -run=^$ -bench=BenchmarkPathological -benchmem
```

For command-level profiles, opt in with either flags or environment variables:

```bash
aha --cpuprofile cpu.pprof --memprofile heap.pprof verify --workspace ~/.aha/corpus
AHA_CPU_PROFILE=cpu.pprof AHA_MEM_PROFILE=heap.pprof aha archive upload && aha archive download
```

Inspect with:

```bash
go tool pprof -http=:0 cpu.pprof
go tool pprof -http=:0 heap.pprof
```

Suggested scenarios:

- Archive: `1000x4KiB`, `100x1MiB`, `5x100MiB` files; compare `Capture`, `Write`, `ReadManifest`, `StreamManifestFiles`.
- Ingest: many sessions/entries/messages/artefacts; first ingest, duplicate ingest, local-depot ingest.
- Search: 100k+ messages plus artefacts; default query, no-hit query, source/role filters, path filter, high limit.
- Depot: local depot with 100/1k/10k catalogue refs; fake-R2 operation counts for list/put/verify.
- Verify/status: large corpus; `Status`, `Verify`, `ReconcileFTS`, `status --archive`.

## Initial benchmark observations

A one-shot smoke benchmark (`go test ./internal/archive ./internal/corpus ./internal/search ./internal/depot -run=^$ -bench=. -benchtime=1x -benchmem`) on an Apple M2 Ultra showed the expected hotspots. These numbers are not stable performance targets, but they are useful directionally:

- `BenchmarkIngestBundle` over a 10-session/1000-entry synthetic bundle took about `120ms` and allocated about `59MB`.
- Before rowid-based FTS verification, `BenchmarkVerify` over a 20-session/2000-entry synthetic corpus took about `1.0s`, and the pathological 5k-message verify took about `7.7s`; after the rowid fix, the 5k pathological benchmark is about `15ms`.
- `BenchmarkReconcileFTS` over the same corpus took about `15ms`.
- Default FTS search over the 2000-entry corpus took about `9ms`; no-hit search was much cheaper.
- Local depot list/verify over small seeded depots was sub-millisecond to a few milliseconds, but complexity remains linear in catalogue refs/bundle bytes.

The main optimisation targets have now landed: rowid FTS verification, known-SHA handoffs, prepared/prefetched ingest, indexed project/path-token search, high-limit guardrails, quick/deep depot verification, operation-budget tests, disk maintenance commands, and catalogue compaction.

## Guardrails for optimisation

Do not trade away these invariants for speed:

- deterministic archive bytes;
- manifest-listed SHA/size validation;
- path/symlink safety;
- append-only/conflict quarantine behaviour;
- content-addressed bundle/object identity;
- raw data preservation;
- repairability of derived FTS state.

Performance work should preserve a deep/strict verification path even when adding fast paths.
