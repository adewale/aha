# Performance, scalability, and longevity plan

Date: 2026-05-25

This plan combines the algorithmic audit in `docs/performance-audit.md`, pathological benchmarks, property-based performance invariants, and pprof observations. The goal is to keep `aha` usable as local histories grow from a few thousand messages to years of multi-agent, multi-machine history without weakening deterministic bundles, content-addressed identity, append-only ingest, or repairability.

## Revised testing strategy

Pathological performance testing now has two layers:

1. **Synthetic benchmarks for byte/row-heavy cases**: large message counts, broad FTS terms, many tiny archive files, and deep depot verification. These expose constant factors and give pprof data.
2. **Property-based performance invariants for metadata-heavy cases**: many trivial bundles, duplicates, many machines, and many catalog shapes. These avoid storing huge datasets and assert complexity by operation/cardinality rather than wall-clock time.

The second layer is especially important for depot/status/refresh behavior because the failure mode is often not a 100MiB object; it is years of tiny no-op snapshots and duplicate catalog refs.

## Pathological benchmark suite

New benchmarks deliberately stress worst-case shapes rather than average fixtures:

```bash
go test ./internal/archive -run=^$ -bench=BenchmarkPathological -benchmem
go test ./internal/corpus  -run=^$ -bench=BenchmarkPathological -benchmem
go test ./internal/search  -run=^$ -bench=BenchmarkPathological -benchmem
go test ./internal/depot   -run=^$ -bench=BenchmarkPathological -benchmem
```

Scale knobs:

- `AHA_PATHOLOGICAL_INGEST_ENTRIES` default `10000`;
- `AHA_PATHOLOGICAL_VERIFY_MESSAGES` default `5000`;
- `AHA_PATHOLOGICAL_RECONCILE_MESSAGES` default `5000`;
- `AHA_PATHOLOGICAL_STATUS_MESSAGES` default `5000`;
- `AHA_PATHOLOGICAL_STATUS_BUNDLES` default `5000`;
- `AHA_PATHOLOGICAL_SEARCH_MESSAGES` default `10000`;
- `AHA_PATHOLOGICAL_DEPOT_REFS` default `250`.

Useful profile commands:

```bash
go test ./internal/corpus -run=^$ -bench='BenchmarkPathologicalVerifyFTSJoinScaling/messages_5000$' -benchtime=1x -cpuprofile=/tmp/aha-verify.cpu -memprofile=/tmp/aha-verify.mem
go test ./internal/corpus -run=^$ -bench='BenchmarkPathologicalIngestManyTinyEntries$' -benchtime=1x -cpuprofile=/tmp/aha-ingest.cpu -memprofile=/tmp/aha-ingest.mem
go test ./internal/search -run=^$ -bench='BenchmarkPathologicalQueryBroadTermPathFilter/broad-term-limit-1000$' -benchtime=50x -cpuprofile=/tmp/aha-search.cpu -memprofile=/tmp/aha-search.mem
go tool pprof -top /tmp/aha-verify.cpu
go tool pprof -top -alloc_space /tmp/aha-ingest.mem
```

## What the first pathological run showed

Machine: Apple M2 Ultra, `go test ... -benchtime=1x -benchmem`. Numbers are directional, not release targets.

| Area | Pathological case | Observation | Interpretation |
|---|---:|---:|---|
| Corpus verify | 1k / 2k / 5k messages | `388ms` / `1.34s` / `7.68s` | Current FTS verification scales worse than linear; likely near quadratic for FTS key joins. |
| FTS repair | 5k messages + artifacts | `43ms` | Full rebuild is much cheaper than the current verifier at this scale. |
| Ingest | 10k tiny entries | `1.23s`, `136MB`, `1.19M allocs` | Many small DB operations and transient parsing/SQL allocations dominate. |
| Search | 10k broad-term messages | `51-57ms`; limit 1000 allocated `1.78MB` / `33k allocs` | Broad terms and high limits scale with candidate/result count; ref formatting and SQLite text extraction are visible. |
| Search path filter | 10k broad-term messages + rare/no path match | `40-42ms` | Non-indexable contains filters still require broad candidate work. |
| Archive | 5000 tiny files | write `39.6ms`, stream `36.9ms`, `190k+ allocs` | Manifest JSON, tar headers, PAX handling, and per-file hashing dominate tiny-file bundles. |
| Archive | one 32MiB compressible file | `25ms`, `52MB allocs` | Large-file path is throughput-oriented; memory comes mostly from zstd buffers/test data. |
| Local depot | 250 refs | list `0.7ms`; put after large catalog `2.0ms`; verify `24.9ms` / `20MB` | Local catalog costs are linear and fine at 250 refs, but deep verify scales with object count/bytes. |
| Status support | 5k messages + 5k bundles | counts `1.9ms`; `BundleSHAs` `1.8ms` / `1.25MB` | `status --depot` set-difference memory grows with ingested bundle count. |

## Property-based performance invariants

For many-trivial-bundle scenarios, tests should generate compact catalog/corpus models and assert invariants such as:

- **cardinality invariants**: pending/behind work is based on unique bundle SHA, not duplicate catalog refs;
- **operation-count invariants**: status/list/state checks may list metadata but must not fetch bundle bytes unless a deep operation is explicitly requested;
- **byte-read invariants**: unchanged refresh should compare catalog `state_sha256` and read `0` old bundle bytes when state metadata exists;
- **bounded-output invariants**: query/status JSON should be bounded by requested limit or unique ref count, not raw duplicate count;
- **idempotence invariants**: repeated trivial bundles do not increase pending ingest work after the first successful ingest;
- **shape invariants**: many machines/shards and many duplicate refs produce the same answer as a set model.

A first example is now covered by `TestDepotBehindFromRefsCountsUniqueCatalogMinusCorpusProperty`: `status --depot` behind counts are computed as `unique(catalog_sha) - ingested_sha`, so duplicate refs from many trivial bundles do not inflate work/counts.

This changes the roadmap: every optimization below should get both a benchmark and a small-model property test when the risk is algorithmic. Benchmarks answer “how expensive is this implementation?”; PBT answers “what must not grow with duplicates, stale refs, or old trivial bundles?”

## Profiling lessons

- `corpus.Verify` is the urgent hotspot. CPU profiles for 5k messages put about 90%+ of time under `corpus.Verify -> verifyCount -> sqlite VDBE`, with FTS5 cursor/column work visible. The verifier joins `messages` to `fts_messages` on unindexed FTS columns, so it does not behave like a normal b-tree join.
- Ingest CPU is mostly SQLite stepping and writes from `ingestEntry`. Allocation profiles show `zstd` encoder/decoder buffers, `parseGenericJSONL`, `database/sql` argument binding, and conflict-detection queries.
- Search broad-term CPU is SQLite/IO dominated. Allocation profiles show result construction and `model.FormatRef`/base64 output becoming visible at high limits.
- Archive many-tiny-file memory profiles show `zstd`, `encoding/json.MarshalIndent`/`Unmarshal`, `normalizeBundleForWrite`, tar header/PAX parsing, and per-file formatting/allocation.
- Depot profiles are syscall-heavy by design: local verify walks/stats/hashes files; catalog append rewrites JSON atomically; deep R2 verify would add network download cost per object.

## How normal usage will degrade over time

| Growth axis | Current behavior | Long-term risk |
|---|---|---|
| More messages | Ingest remains roughly linear in new entries, but verify can become superlinear due FTS key joins. | `aha verify` becomes too slow for routine use and agents stop running it. |
| More broad terms | FTS keeps search usable, but broad/common terms and high `--limit` increase SQL work and output allocations. | Search latency and JSON size grow; agent loops become slower and noisier. |
| More paths/projects | `--path` uses contains matching over cwd/path columns. | Path filters stay non-indexable and degrade when combined with common terms. |
| More bundles | `BundleSHAs`, depot `List`, catalog JSON parse/sort, and status set-difference are linear in unique refs, with PBT guarding duplicate-ref semantics. | `status --depot` and depot ingest startup become increasingly expensive if catalogs grow without summary/compaction. |
| More unchanged refreshes | Refresh can list refs and fetch/read prior same-machine bundles to compare state. | Unchanged daily refresh gets slower over time, especially with R2. |
| More depot objects | Deep verify hashes/downloads every object. | Correct integrity audits become expensive and network-costly. |
| More tiny files | Manifest/tar/header overhead scales with file count, not just bytes. | Many subagent artifacts or small sessions create high allocation/metadata overhead. |
| More years of history | Corpus and bundle blobs are append-only by design. | Disk usage grows monotonically unless users get compaction/export/retention tools. |

## Plan by hotspot

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

Expected impact: this is the highest-priority performance fix. The benchmark already shows 5k messages taking ~7.7s; without this, routine verify will not survive normal long-term growth.

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
4. Add progress output and JSON counters for large depots.
5. Add fake-R2 operation-budget tests for quick verify, deep verify, and repair.

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

1. **Performance contracts in tests**: keep pathological benchmarks non-gating, but add query-plan/static/PBT tests for known algorithmic hazards: unindexed FTS verification, non-indexed exact project filters, accidental network/fetch calls in status/refresh, repeated hash passes where APIs promise known SHA, and duplicate trivial bundles inflating work.
2. **Telemetry-free local metrics**: add `--json` timing/counter fields for expensive commands: files scanned, bytes read, bytes written, bundles listed/fetched, SQL rows inserted, FTS rows repaired.
3. **Disk-growth tools**: add `aha corpus size`, `aha corpus vacuum`, and eventually retention/export policies. Append-only raw preservation remains default; deletion/retention must be explicit.
4. **Depot catalog compaction**: as refs grow, support compacted per-machine catalog snapshots or sharded-by-time catalogs while keeping bundle objects content-addressed.
5. **Background/deferred maintenance**: make deep verify, FTS repair, and vacuum explicit maintenance operations with progress and resumability instead of hidden costs in routine commands.
6. **Scalable defaults**: cap dangerous limits, choose quick checks by default, and require explicit flags for deep/network-heavy work.

## Priority order

1. Fix FTS verification identity (`rowid`/indexed shadow keys). This removes the first observed superlinear cliff.
2. Add PBT/operation-count invariants for many trivial bundles, duplicate catalog refs, and no-op depot/status/refresh paths.
3. Remove depot refresh old-bundle fetches by storing `state_sha256` in catalog refs.
4. Remove duplicate bundle hashing across depot ingest and archive/depot publish.
5. Prepare/batch ingest SQL and prefetch per-session duplicate/conflict state.
6. Add indexed project/path filtering and cap/warn high-limit broad searches.
7. Split depot/status quick vs deep modes and add R2 operation-budget tests.
8. Add disk-size/vacuum/retention tooling for multi-year longevity.

The principle: keep strict/deep integrity operations available, but make routine `refresh`, `search`, `status`, and `verify` scale with metadata or indexed row counts rather than historical bytes and unindexed virtual-table scans.
