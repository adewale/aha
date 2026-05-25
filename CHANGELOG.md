# Changelog

All notable changes to `aha` are documented here. `aha` has not had a tagged release yet; entries under **Unreleased** describe changes currently on the development branch.

## Unreleased

### Added

- R2/local depot model for durable bundle storage.
- `aha depot init|ls|verify`, including `depot verify --repair`.
- `--depot` for `snapshot`, `refresh`, `ingest`, `status`, and `doctor`.
- Local depot default at `~/.aha/depot`.
- Content-addressed bundle layout: `bundles/v1/<bundle_sha256>.tar.zst`.
- Per-machine repairable catalog shards: `catalog/v1/<machine>.json`.
- R2 support through the AWS SDK S3 client with Cloudflare endpoint/region handling.
- Fake-S3 R2 tests plus a real-R2 integration test behind the `integration` build tag.
- Depot-specific fuzz targets for address parsing and bundle-key validation.
- Unified verification entrypoints via `scripts/verify.sh` and Make targets, including bounded fuzz and optional mutation profiles.
- `rapid` property-based testing dependency plus shared test generators for shrinkable invariant tests.
- Static correctness-debt inventory tests for ambient time, raw identity concatenation, manual FTS writes, and direct append-only table mutations.
- Correctness-by-construction guardrails and implementation hardening: typed identity/ref primitives, sealed canonical-only ref variants/parsing/formatting, corpus/depot state-machine skeletons, adapter conformance fixtures, schema introspection helpers, exact canonical read/human resolution split, clock/sleeper/backoff/source-capability seams, corpus verifier/reconciler queries, v2-only bundle schema validation, v2-only session-key construction, targeted SQLite foreign keys/checks, append-only/quarantine triggers, and trigger-maintained FTS rows.
- `status --depot` behind-bundle reporting.
- `doctor --depot` depot diagnostics, including common R2/S3 configuration mistake warnings.
- Refresh idempotency for unchanged source state.
- Property-based coverage for depot contracts, catalog merges, pending-ingest sets, archive path safety, config round trips, source path safety, and search/read coherence.
- `aha verify` for corpus invariant checks, with `--repair-fts` for derived FTS row repair.
- Prior-art improvement and performance audit specs documenting hardening requirements, hotspots, benchmark plans, and optimization guardrails.
- Optional command-level Go pprof output via `--cpuprofile`, `--memprofile`, `AHA_CPU_PROFILE`, and `AHA_MEM_PROFILE`.
- Pathological performance benchmarks for many tiny archive files, large ingest/verify/search/status corpora, and large local depot catalogs.
- Performance scalability/longevity plan tying benchmark/profiling findings to concrete optimization phases.
- Rowid-based FTS verification with query-plan guard, reducing pathological 5k-message verify from seconds to milliseconds.
- Known bundle identity handoffs: `archive.WriteWithInfo`, catalog `state_sha256`/`manifest_sha256`, depot known-SHA publish, and expected-SHA ingest staging.
- Quick/default depot verification with `--deep` for byte/manifests checks; fake-R2 coverage ensures quick verify does not download bundles.

### Changed

- `snapshot` and `refresh` write to the configured depot instead of an output directory.
- No-argument `ingest` pulls pending depot bundles (`catalog - corpus`) instead of globbing an output directory.
- Trust docs now distinguish local-by-default behavior from explicit remote/R2 upload behavior.
- Generated command docs now include depot commands, depot-aware flags, corpus verification, global profiling guidance, and depot quick/deep verification flags.

### Removed

- Removed pre-release `--out` support.
- Removed pre-release `bundle_out_dir` config support.
- Removed local `.receipt.json` sidecars; bundle path/key and SHA are reported through command output/JSON, while durable metadata lives in the manifest and depot catalog.

### Security / correctness

- Depot ingest verifies catalog SHA/key against actual bundle bytes before corpus ingest.
- Local depot catalog updates use locking plus atomic writes.
- R2 catalog updates use conditional writes and retry-on-conflict behavior.
- `depot verify` is quick by default and validates marker/catalog/object-existence metadata; `depot verify --deep` validates bundle bytes/manifests and catalog/object agreement.
- Local depot paths and catalog-derived bundle keys are validated against traversal and malformed-key attacks.
- Network imports are restricted to `internal/depot` by static tests.

### Documentation

- Added architecture docs with aggregation/deduplication walkthroughs, generated command docs, trust documentation updates, R2 bucket-settings guidance, R2 snapshot aggregation spec updates, verification guidance, a corrected correctness-by-construction spec, and a docs consistency audit.
- Added OpenCode research and evaluation docs.
