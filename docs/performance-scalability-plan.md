# Performance, scalability, and longevity plan

Date: 2026-05-25

This plan combines the algorithmic audit in `docs/performance-audit.md`, pathological benchmarks, property-based performance invariants, and pprof observations. The goal is to keep `aha` usable as local histories grow from a few thousand messages to years of multi-agent, multi-machine history without weakening deterministic content-addressed snapshots, append-only ingest, or repairability. Latest captured benchmark results live in `docs/performance-results.md`.

*Scope note (June 2026): the depot rows in this plan predate depot v2 ([docs/depot-v2-spec.md](depot-v2-spec.md)). The v1 bundle/catalog store — monolithic `tar.zst` bundles, catalog shards, `state_sha256` refs, `depot compact` — was replaced by content-addressed snapshots (write-once blobs + per-machine manifests), which remove the bundle-growth axes by construction: push uploads only new file versions, pull fetches only unknown blobs, and steady-state paths never LIST. v1-era measurements and design records are retained below and marked as such; the corpus/search/verify guidance remains current.*

## Revised testing strategy

Pathological performance testing now has two layers:

1. **Synthetic benchmarks for byte/row-heavy cases**: large message counts, broad FTS terms, many tiny archive files, and deep depot verification. These expose constant factors and give pprof data.
2. **Property-based performance invariants for metadata-heavy cases**: many trivial bundles, duplicates, many machines, and many catalog shapes. These avoid storing huge datasets and assert complexity by operation/cardinality rather than wall-clock time.

The second layer is especially important for depot/status/refresh behavior because the failure mode is often not a 100MiB object; it is years of tiny no-op snapshots (and, in the v1 depot, duplicate catalog refs — depot v2's operation-count properties play the same role for pointer/manifest GETs and blob fetches).

## Layer-selection rules

Use the cheapest layer that can falsify the performance claim:

1. **Pure/model PBT first** for set semantics, deduplication, idempotence, ordering, cardinality, and “must not become work” claims. Example: pending/behind refs are `unique(catalog_sha) - ingested_sha` even when generators produce many duplicate trivial bundles.
2. **Fake-driver operation-count tests** for network, fetch, byte-read, and API-call claims. Example: `status --depot` may list metadata once, but must not fetch bundle bytes.
3. **Tiny SQLite/query-plan tests** for schema/index claims. Example: verifier/search regressions should fail because `EXPLAIN QUERY PLAN` shows an unindexed/virtual-table scan, not because a 5k-row benchmark got slow.
4. **Package benchmarks** for constant factors and row/byte-heavy loops. Example: `corpus.Verify`, `search.Query`, `archive.Write`, and `depot.Verify` benchmarks are useful once the algorithmic invariant is correct.
5. **Package-level pprof** before CLI-level pprof. Profile the smallest benchmark that reproduces the cost; use CLI profiles only to confirm end-to-end command behavior.
6. **Large real datasets last** as smoke/perception tests. They are valuable before release, but too expensive and too opaque to be the primary guard.

Wall-clock assertions should almost never be unit tests. Prefer deterministic counters: unique refs, fetch calls, bytes read, SQL query plans, rows touched, output cardinality, and idempotent state transitions.

## Phase 0: abstraction-readiness characterization

Before changing an abstraction for performance, add a characterization gate that freezes the externally important behavior and the performance invariant the new abstraction is supposed to improve. This is the minimum test net needed to reconsider internals freely. (The depot rows in the table below are v1-era records; depot v2 deleted those abstractions — catalog `state_sha256` refs, `WriteWithInfo` bundle handoff, expected-SHA bundle ingest, and catalog compaction — outright.)

| Proposed abstraction change | Correctness characterization | Performance/scalability characterization | Metric that should improve |
|---|---|---|---|
| FTS `rowid` / indexed shadow-key verifier | Seed tiny corpora with matching rows, missing FTS rows, orphan FTS rows, artifact text rows, and repaired rows; assert `Verify`/`ReconcileFTS` reports are unchanged. | Tiny `EXPLAIN QUERY PLAN`/schema guard that verifier uses indexed row identity rather than scanning unindexed FTS columns; keep pathological verify benchmark for magnitude. | `BenchmarkPathologicalVerifyFTSJoinScaling` slope and `ns/op`; query-plan scan count. |
| Catalog `state_sha256` refs | State signature equality remains `ManifestStateSHA256`-equivalent and ignores volatile bundle metadata; catalog repair preserves bundle truth. | Fake-driver/PBT over refs with duplicates, many machines, stale ordering: unchanged refresh performs `0` `Fetch` calls and reads `0` old bundle bytes when state metadata exists. | Old bundle fetch count, old bundle bytes read, unchanged `refresh` latency. |
| `archive.WriteWithInfo` known-SHA handoff | Golden bundles remain byte-identical; returned SHA/size/manifest/state fields equal independently recomputed values. | Unit/operation test proves depot publish can accept writer-produced identity without rehashing the finished bundle. | Compressed-bundle read passes after write; snapshot/refresh publish bytes read. |
| `corpus.IngestBundleWithExpectedSHA` | Expected SHA mismatch fails before promotion; duplicate/corrupt bundle behavior and JSON errors stay unchanged. | Fake/local operation test proves depot ingest performs one staging copy/hash and no separate pre-hash. | Bundle read passes per pending depot ingest; ingest wall time for large pending bundles. |
| Prepared/batched ingest writer | Corpus rows, conflicts, FTS rows, image/artifact blobs, and append-only behavior match existing ingest on representative fixtures. | Small-model duplicate/conflict PBT plus package benchmark tracking `allocs/entry` and SQL calls/entry. | `allocs/op`, `allocs/entry`, SQL executions/entry, ingest `ns/op`. |
| Indexed project/path search | Search/read coherence and deterministic ordering stay unchanged; exact project/path filters return the same logical subset as old contains where applicable. | Tiny query-plan tests for indexed exact filters; broad-term/path benchmarks for magnitude. | Query plan, broad query `ns/op`, high-limit allocs, path-filter latency. |
| Quick/deep depot verify | Deep verify still detects corrupt/missing/mismatched bundle bytes; quick verify reports metadata/catalog problems only and says what it did not check. | Fake-R2 operation-budget tests: quick uses list/head metadata only; deep downloads/hashes; repair has explicit cost. | R2 GET/download count, bytes downloaded, local bytes hashed, verify latency. |
| Depot/status summaries or compaction | `depot ls`, pending ingest, repair, and status answers match the uncompacted catalog model. | PBT over many trivial bundles/machines/shards; in-memory catalog benchmarks before/after map-backed merge. | Catalog rows scanned/read, JSON bytes parsed, refs merged/sec, status latency. |

If a row lacks a characterization gate, the abstraction is not ready to change yet. This is the direct application of the testing-best-practices rule: make illegal states unrepresentable when possible, then keep only invariant-proof and model-gap tests at the boundary where the type/schema stops helping.

## Cheapest-layer audit results

*v1-era audit record: the depot rows below (trivial bundles, `status --depot` metadata, `state_sha256` no-fetch, expected-SHA ingest, `PutBundleKnown`) describe v1 machinery that depot v2 deleted; their named tests/helpers no longer exist. The corpus/search/profiling rows remain current.*

| Risk | Cheapest effective layer | Current status | Next cheapest guard |
|---|---|---|---|
| FTS verify superlinear behavior | Schema/query-plan tests over tiny corpora; package benchmark only for magnitude/profile | Implemented: FTS rows use rowid identity; `TestVerifyFTSChecksUseRowIDLookups` guards the query shape; 5k pathological verify dropped from ~`7.7s` to ~`15ms` on the same machine class. | Keep query-plan guard; use benchmarks only for trend checks. |
| Many trivial depot bundles / duplicate refs | Pure set-model PBT and in-memory catalog merge benchmark | Implemented: `pendingDepotRefs`, `depotBehindFromRefs`, and `MergeBundleRefs` have set/metadata properties; repair/compact use map-backed bulk merge. | Keep benchmark trend checks as catalogs grow. |
| `status --depot` should be metadata-only | Helper/fake-driver operation-count unit test | Implemented: `TestDepotBehindCountFromDriverListsMetadataWithoutFetchingBundles` proves one list and zero fetches; status JSON now exposes listed/unique refs and `depot_fetches=0`. | Add summaries only if raw catalog scans become visible on real depots. |
| Unchanged refresh should not fetch old bundles | Fake-driver/PBT over refs with `state_sha256` metadata | Implemented: catalog refs carry `state_sha256`; `findDepotBundleWithSameState` checks it before fetching; fake-driver test proves zero fetches for matching state metadata. | Add broader PBT over duplicate refs/machines/orderings. |
| Depot ingest duplicate/pre-hash behavior | Pending-set PBT plus expected-SHA ingest seam tests | Implemented: `corpus.IngestBundleWithExpectedSHA` validates during staging; depot ingest no longer pre-hashes separately. | Add byte-counter instrumentation for large pending bundles. |
| Archive repeated hashing | Unit/golden test for `WriteWithInfo` SHA handoff | Implemented: `archive.WriteWithInfo` streams compressed SHA/size/manifest/state info; CLI depot publish uses `depot.PutBundleKnown`. | Add byte-counter instrumentation for snapshot/refresh publish. |
| Ingest per-entry SQL chatter | Package benchmark plus small-model duplicate/idempotence PBT | Implemented: transaction-scoped prepared statements, session-local existing-entry prefetch, cross-machine conflict prefetch, indexed query-plan guard, duplicate-bundle parse-skip test, known-file-blob no-recompress test, and zstd encoder pooling for file blobs. | True multi-row insert batching remains an optional future constant-factor optimization; correctness/perf guards are in place. |
| Search path/filter cost | Query-plan tests and `search.Query` benchmarks | Implemented: `sessions(project_key)` index, `--project`, indexed `session_path_tokens`/`artifact_path_tokens`, `--path-token`, actual SQL query-plan guards, artifact coverage, and high-limit capping/warnings. | Keep `--path` as documented slow contains filter for convenience. |
| Profiling | Package-level pprof first; CLI pprof only for end-to-end confirmation | Optional CLI profiling exists; benchmark profiles were more useful for root cause | Keep CLI pprof opt-in; prefer package benchmark profiles for optimization loops. |

## Pathological benchmark suite

New benchmarks deliberately stress worst-case shapes rather than average fixtures:

```bash
go test ./internal/archive -run=^$ -bench=BenchmarkPathological -benchmem
go test ./internal/corpus  -run=^$ -bench=BenchmarkPathological -benchmem
go test ./internal/search  -run=^$ -bench=BenchmarkPathological -benchmem
```

(The v1 depot benchmarks — catalog merge, large-catalog list/verify — were deleted with the v1 bundle store; depot v2 paths are guarded by operation-count tests rather than benchmarks.)

Scale knobs:

- `AHA_PATHOLOGICAL_INGEST_ENTRIES` default `10000`; set `AHA_PATHOLOGICAL_INGEST_LARGE` to also run 50k/100k ingest cases;
- `AHA_PATHOLOGICAL_VERIFY_MESSAGES` default `5000`;
- `AHA_PATHOLOGICAL_RECONCILE_MESSAGES` default `5000`;
- `AHA_PATHOLOGICAL_STATUS_MESSAGES` default `5000`;
- `AHA_PATHOLOGICAL_STATUS_BUNDLES` default `5000` (seeds extra snapshots for status scaling);
- `AHA_PATHOLOGICAL_SEARCH_MESSAGES` default `10000`.

Useful profile commands:

```bash
go test ./internal/corpus -run=^$ -bench='BenchmarkPathologicalVerifyFTSJoinScaling/messages_5000$' -benchtime=1x -cpuprofile=/tmp/aha-verify.cpu -memprofile=/tmp/aha-verify.mem
go test ./internal/corpus -run=^$ -bench='BenchmarkPathologicalIngestManyTinyEntries$' -benchtime=1x -cpuprofile=/tmp/aha-ingest.cpu -memprofile=/tmp/aha-ingest.mem
go test ./internal/search -run=^$ -bench='BenchmarkPathologicalQueryBroadTermPathFilter/broad-term-limit-1000$' -benchtime=50x -cpuprofile=/tmp/aha-search.cpu -memprofile=/tmp/aha-search.mem
go tool pprof -top /tmp/aha-verify.cpu
go tool pprof -top -alloc_space /tmp/aha-ingest.mem
```

## What the first pathological run showed (historical baseline)

Machine: Apple M2 Ultra, `go test ... -benchtime=1x -benchmem`. Numbers are directional, not release targets. This section is preserved as the pre-plan baseline; later results are in the next section and in `docs/performance-results.md`. The depot/catalog rows measured the v1 bundle store.

| Area | Pathological case | Observation | Interpretation |
|---|---:|---:|---|
| Corpus verify | 1k / 2k / 5k messages | `388ms` / `1.34s` / `7.68s` | Baseline FTS verification scaled worse than linear due key joins; rowid-backed verification fixed this cliff. |
| FTS repair | 5k messages + artifacts | `43ms` | In the baseline, full rebuild was cheaper than the old verifier; after rowid verification, repair remains an explicit maintenance path rather than the fast check. |
| Ingest | 10k tiny entries | `1.23s`, `136MB`, `1.19M allocs` | Many small DB operations and transient parsing/SQL allocations dominate. |
| Search | 10k broad-term messages | `51-57ms`; limit 1000 allocated `1.78MB` / `33k allocs` | Broad terms and high limits scale with candidate/result count; ref formatting and SQLite text extraction are visible. |
| Search path filter | 10k broad-term messages + rare/no path match | `40-42ms` | Non-indexable contains filters still require broad candidate work. |
| Archive | 5000 tiny files | write `39.6ms`, stream `36.9ms`, `190k+ allocs` | Manifest JSON, tar headers, PAX handling, and per-file hashing dominate tiny-file bundles. |
| Archive | one 32MiB compressible file | `25ms`, `52MB allocs` | Large-file path is throughput-oriented; memory comes mostly from zstd buffers/test data. |
| Local depot | 250 refs | list `0.7ms`; append to growing catalog `2.0ms`; verify `24.9ms` / `20MB` | Local catalog costs are acceptable at 250 refs, but deep verify scales with object count/bytes. |
| In-memory catalog merge | 1000 unique trivial refs × 4 duplicates | single-digit milliseconds, `1.24MB` | Cheapest-layer benchmark shows bulk merge is linear-scan based and should become map-based before very large catalogs. |
| Status support | 5k messages + 5k bundles | counts `1.9ms`; `BundleSHAs` `1.8ms` / `1.25MB` | `status --depot` set-difference memory grows with ingested bundle count. |

## Latest post-plan benchmark snapshot

Machine: Apple M2 Ultra, `go test ... -benchtime=1x -benchmem` unless noted. These are not CI thresholds; they are before/after signals for the abstractions added by this plan. (v1-era capture: the depot merge and status/`BundleSHAs` rows measured v1 catalog machinery that depot v2 has since deleted.)

| Area | Case | Latest observation | Change vs first pathological run |
|---|---:|---:|---|
| Corpus verify | 5k messages | `16.2ms`, `7.5KB`, `188 allocs` | ~`7.68s` -> ~`16ms` via rowid-backed FTS verification; extra allocs are from user-facing stats counters. |
| Ingest | 10k tiny entries | `1.12s`, `84.1MB`, `728k allocs` | Memory down from `136MB`; time improved vs first `1.23s` run but regressed vs an intermediate `1.01s` run after adding path-token maintenance. |
| Search | 10k broad term, limit requested 1000 | `50.6ms`, `344KB`, `6.7k allocs` | Output is capped at 200, reducing high-limit allocations from the original `1.78MB`/`33k allocs`; broad FTS time remains SQLite/candidate dominated. |
| Search | 10k path-token rare match | `42.7ms`, `43KB`, `729 allocs` | Indexed token filter is available and guarded; broad common-term FTS still dominates this synthetic case. |
| Search | 20×100 normal bench, path-token filter | `7.17ms`, `38.6KB`, `730 allocs` over `10x` | Similar to path/project filters with exact indexed semantics. |
| Depot merge | 1000 unique refs × 4 duplicates | `1.51ms`, `1.49MB`, `9.8k allocs` | Map-backed merge prevents quadratic duplicate behavior and is used by repair/compact. |
| Local depot verify | 250 refs | `28.2ms`, `21.6MB` | Deep verify remains explicitly byte-linear. |
| Status support | 5k messages + 5k bundles | counts `2.0ms`; `BundleSHAs` `1.66ms` / `1.25MB` | Status remains metadata-only; JSON exposes listed/unique depot refs and zero fetches. |

## Metrics and measurable improvement targets

The plan should produce improvements we can point to. Treat these as scenario metrics, not hard CI thresholds until benchmark variance is understood. The depot rows below are v1-era targets, retained as the record of what this plan achieved; depot v2 then removed the underlying mechanisms entirely (no catalog refs, no `state_sha256`, no pending-bundle fetches, no `depot compact` — a steady-state v2 refresh is a handful of pointer/manifest GETs with zero PUTs/fetches/parses, and `status --depot` reports `depot_behind_snapshots`/`depot_machines_listed`).

| Scenario / user journey | Current pain signal | Primary metric | Expected measurable improvement |
|---|---|---|---|
| `aha verify` on a growing corpus | Before rowid identity, 5k-message pathological verify took about `7.7s`; after the fix, the same benchmark took about `15ms`. | `BenchmarkPathologicalVerifyFTSJoinScaling` slope, `ns/op`, and query-plan guard. | Maintain near-linear verification and prevent regressions to unindexed FTS joins. |
| Routine no-op `aha refresh` with local/R2 depot | Old refs without state metadata still require fallback fetch; new refs carry state metadata. | Old bundle `Fetch` calls, old bundle bytes read, unchanged-refresh latency. | New catalog refs allow no-op refresh to list metadata but fetch `0` old bundles when a matching `state_sha256` exists. |
| `aha ingest --depot` with pending bundles | Expected SHA is now checked during ingest staging instead of by a separate pre-hash. | Bundle read/hash passes per pending ref; ingest `ns/op` for large bundles. | Pending depot ingest performs one staging copy/hash, not a pre-hash plus staging hash. |
| `aha snapshot` / `aha refresh` publish | `WriteWithInfo` now computes compressed bundle SHA while writing; known identity is handed to depot. | Compressed bundle bytes read after write; publish `ns/op`; bundle hash pass count. | Snapshot/refresh publish avoids re-reading the just-written bundle for archive/depot identity. |
| Many tiny entries in one ingest | First 10k-entry ingest took about `1.23s`, `136MB`, `1.19M allocs`; latest captured result is `1.12s`, `84.1MB`, `728k allocs`. | `allocs/entry`, SQL executions/entry, ingest `ns/op`. | Prepared statements, duplicate/conflict prefetch, known-blob skip, and zstd pooling lower memory and SQL chatter while reports/conflicts remain identical. |
| Broad `aha search` / high `--limit` | First 10k broad-term high-limit search allocated `1.78MB`/`33k allocs`; latest capped high-limit search is `344KB`/`6.7k allocs`. | Search `ns/op`, allocations/result, output size, query plan. | Exact indexed filters and sane limits reduce output/allocation cost for common agent workflows; broad FTS candidate work is still term-selectivity bound. |
| `aha status --depot` on years of trivial bundles | Behind calculation and catalog parsing scale with raw catalog rows; duplicates must not inflate work units. | Unique work units, raw refs scanned, R2 list/fetch counts, JSON bytes parsed. | Status remains metadata-only (`0` fetches); duplicate refs do not change counts; future summaries/compaction reduce raw metadata scanned. |
| `aha depot verify` on local/R2 depot | Quick/default verify now checks metadata/existence; `--deep`/`--repair` retains full object reads. | R2 GET/download count, bytes downloaded, local bytes hashed, verify latency. | Quick verify performs metadata/head checks only; deep verify remains explicit and reports its cost. |
| Multi-year local use | Append-only bundles/corpus grow monotonically. | Corpus/depot disk bytes, bundle count, catalog bytes, vacuum/compaction/prune reclaimed bytes. | `aha corpus size`, `vacuum`, dry-run/forced `prune-orphans`, and `aha depot compact` make growth visible and maintenance explicit without weakening raw preservation defaults. |

Instrumentation to add as optimizations land:

- benchmark names and `benchstat` comparisons for package hot paths;
- fake-driver counters: `List`, `Fetch`, R2 `Head/Get/List/Put`, bytes downloaded;
- local byte counters for bundle/file hash passes;
- JSON command counters now include depot verify bytes read/downloaded, status listed/unique refs/fetches, verify row counts, FTS rows verified, and FTS repair rows;
- query-plan assertions for verifier/search paths.

## Property-based performance invariants

For many-snapshot scenarios, tests should generate compact depot/corpus models and assert invariants such as:

- **cardinality invariants**: behind/pending work is based on unknown snapshot manifests and unknown file versions, never on re-counting known ones;
- **operation-count invariants**: push/pull/status may GET pointers and manifests but must never LIST objects or fetch known blobs; deep work is explicit (`depot verify --deep`);
- **byte-read invariants**: an unchanged push diffs against the parent manifest and performs zero writes; an in-sync pull fetches zero blobs;
- **bounded-output invariants**: query/status JSON should be bounded by requested limit or unique-version count, not raw history size;
- **idempotence invariants**: re-pushing unchanged state and re-pulling a known snapshot are no-ops;
- **shape invariants**: many machines/manifests produce the same answer as a set model.

Current examples (v2 operation-count/property tests replaced the deleted v1 catalog tests):

- `TestPushV2UnchangedStateIsReusedWithoutWritesOrReads` / `TestV2R2SecondUnchangedPushWritesNothing`: an unchanged push performs zero PUTs and zero blob reads.
- `TestPushV2DeltaReadsAndUploadsOnlyNewContent` / `TestV2R2DeltaPushUploadsOnlyTheDelta`: one new file means exactly that blob plus one manifest and one pointer write.
- `TestV2R2PushOperationInvariants`: push performs zero foreign-prefix GETs and zero LISTs.
- `TestUnchangedSessionFileSkipsReparseAcrossBundles`: a session-file version already proven present is never re-parsed.
- `TestKnownFileBlobSkipsRecompression`: known file blobs are not recompressed/re-written.

This changes the roadmap: every optimization below should get both a benchmark and a small-model property/operation test when the risk is algorithmic. Benchmarks answer “how expensive is this implementation?”; PBT/counter tests answer “what must not grow with duplicates, stale snapshots, old history, or remote object bytes?”

## Profiling lessons

- `corpus.Verify` was the urgent hotspot. CPU profiles for 5k messages put about 90%+ of time under `corpus.Verify -> verifyCount -> sqlite VDBE`, with FTS5 cursor/column work visible. The verifier used to join `messages` to `fts_messages` on unindexed FTS columns; it now uses FTS rowid identity and has a tiny query-plan guard.
- Ingest CPU is mostly SQLite stepping and writes from `ingestEntry`. Allocation profiles show `zstd` encoder/decoder buffers, `parseGenericJSONL`, `database/sql` argument binding, and conflict-detection queries.
- Search broad-term CPU is SQLite/IO dominated. Allocation profiles show result construction and `model.FormatRef`/base64 output becoming visible at high limits.
- Archive many-tiny-file memory profiles show `zstd`, `encoding/json.MarshalIndent`/`Unmarshal`, `normalizeBundleForWrite`, tar header/PAX parsing, and per-file formatting/allocation.
- Depot profiles are syscall-heavy by design: local verify walks/stats/hashes files; catalog append rewrites JSON atomically; deep R2 verify would add network download cost per object.

## How normal usage will degrade over time

| Growth axis | Current behavior | Long-term risk |
|---|---|---|
| More messages | Ingest remains roughly linear in new entries; rowid-backed FTS verification removed the first superlinear verify cliff. | Future schema/FTS changes could reintroduce unindexed verifier work, so query-plan guards must stay green. |
| More broad terms | FTS keeps search usable, but broad/common terms and high `--limit` increase SQL work and output allocations. | Search latency and JSON size grow; agent loops become slower and noisier. |
| More paths/projects | `--path` uses contains matching over cwd/path columns. | Path filters stay non-indexable and degrade when combined with common terms. |
| More snapshots/machines | Push/pull/status read one pointer + one manifest per machine and never LIST; the corpus diffs each manifest against known `(machine, path, sha)` versions. | Work grows with machine count and day's delta, not with history; very large fleets would grow the per-refresh GET count linearly in machines. |
| More unchanged refreshes | Push diffs against the parent manifest; an unchanged machine performs zero writes, and an in-sync pull fetches zero blobs. | A steady-state refresh stays a handful of small GETs; the advisory capture cache keeps the local scan O(changed files), and a wiped cache costs one full re-hash, never re-uploads. |
| More depot objects | `depot verify --deep` fetches and hashes every referenced blob (many small GETs). | Correct integrity audits stay byte-linear and get slower as unique history grows; they remain explicit, never part of daily paths. |
| More tiny files | Per-file blob and manifest-entry overhead scales with file count, not just bytes (tar/header overhead now applies only to `export`). | Many subagent artifacts or small sessions create many small objects and manifest entries; R2's cheap op classes make this affordable, but manifests grow with file count. |
| More years of history | Corpus and depot blobs are append-only by design; the depot stores each unique file version once and never deletes (no GC, by policy). | Disk usage grows with unique bytes — the theoretical floor; growing JSONL files still store one whole blob per observed version until an append-segment refinement lands. |

## Historical implementation plan by hotspot

The hotspot plan below is retained as a design record. The original actions have mostly landed; the current status and remaining watch list are summarized in the final priority section.

### 1. Repeated full-file/full-bundle hashing

Target complexity: keep deep verification `O(bytes)`, but avoid extra `O(bytes)` passes in normal successful paths.

Actions:

1. Add an internal `archive.WriteWithInfo` result: `{Path, BundleSHA256, SizeBytes, ManifestSHA256, StateSHA256}`.
2. Compute compressed bundle SHA while writing by wrapping the final writer in `io.MultiWriter` instead of reopening the finished bundle.
3. Add depot `PutBundleKnown(ref, path)` / `PutBundleWithInfo(info)` so depot publish does not rehash when the archive writer just produced the bytes.
4. Keep `depot verify --deep` and ingest SHA validation as independent integrity checks.
5. Add regression tests proving the single-pass SHA equals `archive.FileSHA256` on deterministic golden bundles.

Expected impact: removes one full compressed-bundle read per snapshot/refresh publish and establishes a reusable known-SHA handoff for depot ingest.

### 2. Unchanged refresh fetching/hashing old depot bundles

Target complexity: unchanged refresh should be `O(catalog refs)` locally and `O(catalog listing)` remotely, not `O(old bundle bytes)`.

Actions:

1. Store `state_sha256`, `manifest_sha256`, bundle size, captured timestamp, source counts, and implementation version in catalog refs.
2. Make `findDepotBundleWithSameState` compare catalog `state_sha256` first.
3. Fetch/read an old bundle only if the catalog ref lacks state metadata or the catalog needs repair.
4. Since this is pre-release, prefer a clean catalog schema bump or a tested additive field strategy over compatibility shims.
5. Add fake-R2 operation-count and PBT model tests: unchanged refresh must not call `Fetch` when catalog state metadata is present, regardless of duplicate refs, machine distribution, or catalog ordering.

Expected impact: repeated no-op refresh stops getting slower as depot history grows.

### 3. Depot ingest pre-hashes before corpus ingest hashes again

Target complexity: one local bundle copy/hash before ingest commit, not two.

Actions:

1. Add `corpus.IngestBundleWithExpectedSHA(path, expectedSHA)`.
2. Thread expected SHA from depot catalog into the ingest planner.
3. Let the planner's staging copy/hash be the authoritative compare point.
4. Remove the separate `archive.FileSHA256(path)` in `ingestFromDepot` for refs with validated catalog keys.
5. Preserve explicit deep verification in `depot verify` and corrupted-download tests.

Expected impact: removes one full-bundle read per pending depot bundle.

### 4. Ingest spooling/hashing/compression repeated reads

Target complexity: ingest should be `O(bundle bytes + entries)`, with low constant factors and bounded temporary space.

Actions:

1. Prepare SQL statements once per ingest transaction for entries, messages, assets, sessions, files, conflicts, and conflict-detection queries.
2. Prefetch existing entry hashes for a session into a map before inserting that session's entries.
3. Prefetch cross-machine conflict candidates per `(source, source_session_id, entry_id)` batch or add an index tuned for the current query.
4. Avoid recompressing file blobs when `files.file_sha256` already exists before spooling/compressing derived blobs.
5. Pool or reuse zstd encoders/decoders where safe; measure before/after because zstd buffers are large.
6. Consider a streaming parser path for session files so large JSONL sessions do not require parse materialization before inserts. Keep bundle-file hashing and raw blob preservation intact.

Expected impact: lowers allocations and syscall/SQLite overhead for many tiny entries while preserving append-only conflict behavior.

### 5. Per-entry SQL chatter

Target complexity: still linear in new entries, but with fewer round trips and fewer allocations per row.

Actions:

1. Add `corpusWriter` prepared-statement lifecycle scoped to the transaction.
2. Replace `tx.QueryRow` duplicate checks with a session-local existing-entry map.
3. Add/index `sessions(source_name, source_session_id, machine_id)` plus `entries(session_key, entry_id, entry_sha256)` coverage for cross-machine checks if query plans show table walks.
4. Batch insert messages where possible, letting triggers maintain FTS rows.
5. Add ingest benchmarks at 10k/50k/100k entries and track `allocs/entry`.
6. Add PBT small-model tests for many trivial bundles: repeated already-ingested bundle refs must not trigger parsing/fetching or increase pending work.

Expected impact: keeps ingest predictable as sessions get large.

### 6. Corpus verify FTS/left-join scans

Target complexity: verifier should be `O(messages + artifacts)` with indexed existence checks, not FTS-table scans on unindexed columns.

Actions:

1. Make FTS row identity b-tree/index-friendly. Preferred pre-release schema change:
   - add integer `message_id`/`entry_rowid` for messages;
   - insert `fts_messages` rows with `rowid = message_id`;
   - insert `fts_artifacts` rows with `rowid = artifact_id` instead of storing `artifact_id` as an unindexed column.
2. Rewrite verify queries to compare `messages.message_id`/`artifacts.artifact_id` to FTS `rowid`.
3. If schema churn is not desired, maintain b-tree shadow key tables for FTS rows and verify against those; less elegant, but still indexed.
4. Add `EXPLAIN QUERY PLAN` assertions for verifier queries so future changes do not reintroduce virtual-table scans.
5. Keep `verify --repair-fts` as full rebuild; add scoped repair only after rowid-based verification is stable.

Impact achieved: this was the highest-priority performance fix. The 5k pathological verifier dropped from about `7.7s` to about `15–16ms`, and query-plan tests now guard against returning to unindexed FTS joins.

### 7. Non-indexable path filters

Target complexity: path/project filters should be indexed or explicitly documented as slow contains filters.

Actions:

1. Add indexed `sessions(project_key)` and expose `--project PROJECT_KEY` for exact/project-level filtering.
2. Add normalized path token table or path FTS (`session_paths_fts`, `artifact_paths_fts`) for path-segment search.
3. Keep `--path contains` for convenience, but warn in docs and maybe cap broad-term/high-limit combinations.
4. Reorder search plans where possible: exact project/source/role/time filters should reduce candidates before FTS ranking when SQLite query plans support it.
5. Add path-filter query-plan tests and benchmarks with common-term/no-match cases.

Expected impact: common project/path workflows remain fast while arbitrary contains remains available as an explicit slow path.

### 8. Depot verify is intentionally linear/deep

Target complexity: quick verification should be `O(catalog refs + metadata)`, deep verification remains `O(bundle bytes)` and explicit.

Actions:

1. Split depot verification modes:
   - quick/default: marker, catalog schema, key shape, duplicate refs, local object existence/head metadata;
   - deep: hash/read every bundle and validate manifest;
   - repair: rebuild catalog from objects when local/deep enough data is available.
2. For R2, use object metadata/head where possible before downloads.
3. Store/refuse inconsistent catalog fields (`bundle_sha256`, `key`, `size`, `manifest_sha256`, `state_sha256`).
4. Implemented: typed stderr progress for large depots (`auto|off|plain|tty|json`), with machine/blob/byte counters only when honestly observable; final JSON remains isolated on stdout.
5. Add fake-R2 operation-budget tests for quick verify, deep verify, and repair.
6. Replace linear-scan bulk catalog merging with map-backed merge for repair/compaction paths; keep append of one ref simple unless benchmarks show it dominates.

Expected impact: users can run cheap health checks often and reserve deep audits for scheduled/manual integrity checks.

### 9. `status --depot` network catalog operation

Target complexity: default status stays local and cheap; depot status has explicit quick/deep semantics.

Actions:

1. Keep plain `aha status` corpus-only.
2. Make `status --depot` report that it lists the depot; document that R2 may incur network calls.
3. Add optional depot summary files/counters so `status --depot --quick` can avoid reading every shard when available.
4. Cache last depot status in the local corpus metadata with timestamp and depot address; show stale age clearly.
5. Keep `aha depot ls` / `aha depot verify --deep` for exact current answers.

Expected impact: routine status remains fast; remote status cost becomes predictable and visible.

## Cross-cutting longevity work

1. **Cheapest-layer gate before optimization**: before changing a hot path, state the claim and choose the cheapest falsifier: pure PBT, fake-driver counter, tiny query-plan test, package benchmark, package profile, or CLI profile. Do not start with large fixtures unless the claim is specifically about real-world perception.
2. **Performance contracts in tests**: keep pathological benchmarks non-gating, but add query-plan/static/PBT tests for known algorithmic hazards: unindexed FTS verification, non-indexed exact project filters, accidental network/fetch calls in status/refresh, repeated hash passes where APIs promise known SHA, and duplicate trivial bundles inflating work.
3. **Telemetry-free local metrics**: add `--json` timing/counter fields for expensive commands: files scanned, bytes read, bytes written, bundles listed/fetched, SQL rows inserted, FTS rows repaired.
4. **Disk-growth tools**: add `aha corpus size`, `aha corpus vacuum`, and eventually retention/export policies. Append-only raw preservation remains default; deletion/retention must be explicit.
5. **Depot catalog compaction**: as refs grow, support compacted per-machine catalog snapshots or sharded-by-time catalogs while keeping bundle objects content-addressed.
6. **Background/deferred maintenance**: deep verify, FTS repair, vacuum, and corpus rebuild are explicit maintenance operations; typed progress and context cancellation are implemented. Snapshot/depot/rebuild operations are idempotent, but checkpoint-based mid-operation resume remains future work.
7. **Scalable defaults**: cap dangerous limits, choose quick checks by default, and require explicit flags for deep/network-heavy work.

## Priority order

Completed from the original priority list:

- FTS verification identity now uses rowid and has a query-plan guard.
- PBT/operation-count invariants cover duplicate depot refs, map-backed catalog merge, status metadata-only behavior, duplicate ingest parse skipping, known file blob no-recompression, and no-op refresh state metadata.
- New catalog refs carry `state_sha256`/`manifest_sha256`.
- Archive/depot/ingest known-SHA handoffs remove redundant hot-path hashing.
- Ingest uses transaction-scoped prepared statements, prefetches same-session and cross-machine conflict state, has a covering query-plan guard, skips known file-blob recompression, and pools zstd encoders for file blobs.
- Search has exact indexed `--project`, indexed `--path-token` for sessions/artifacts, a capped `--limit` (`200`) with CLI warning, actual-SQL query-plan guards, and artifact filter coverage. `--path` remains a documented contains filter.
- Depot verify has quick/default and deep/repair paths, with fake-R2 operation-budget coverage for quick, deep, repair, list, and compact. Verify JSON reports bytes read/downloaded.
- Disk/longevity tooling exists: `aha corpus size`, `aha corpus vacuum`, dry-run/forced `aha corpus prune-orphans`, and `aha depot compact`.

Remaining watch list, not blockers for this plan:

1. True multi-row SQL `INSERT` batching could reduce ingest constant factors further, but prepared statements and prefetching already changed the algorithmic shape and are covered by tests.
2. Search broad/common terms are still FTS-candidate bound; exact filters reduce output/allocation cost and are indexed, but SQLite still ranks broad terms.
3. Catalog summary files/local stale caches should be added only if real depots show raw catalog scanning as a bottleneck; `compact` and map-backed merge are in place.
4. Deletion/retention beyond orphan pruning remains a product-policy decision because raw preservation is the default trust model.
5. More command-level JSON counters can be added for SQL row insert counts if agent workflows need them; verify/FTS/depot/status counters are in place.

The principle: keep strict/deep integrity operations available, but make routine `refresh`, `search`, `status`, and `verify` scale with metadata or indexed row counts rather than historical bytes and unindexed virtual-table scans.
