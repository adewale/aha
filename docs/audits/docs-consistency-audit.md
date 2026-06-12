# Documentation Consistency Audit

Date: 2026-05-19
Updated: 2026-05-25

*Historical: predates depot v2 ([../depot-v2-spec.md](../depot-v2-spec.md)). The bundle/catalog vocabulary and depot mechanics this audit normalized were replaced by content-addressed snapshots (blobs + manifests + pointers) in June 2026.*

Scope: README, changelog, command docs, trust docs, user journeys, architecture, R2 snapshot aggregation spec, correctness specs, performance audit, performance results, lessons learned, and the original aggregator spec.

## Checks performed

- Current CLI nouns are consistent: **bundle**, **depot**, **catalog**, **corpus**, **ref**.
- Removed pre-release `--out` / `bundle_out_dir` from current user-facing docs and config examples.
- Removed local receipt sidecar requirements from current architecture/trust/spec text; `CHANGELOG.md` documents the removal.
- Verified `docs/commands.md` remains generated from command metadata.
- Consolidated architecture explanation so command flows and aggregation/deduplication are described in one place instead of repeated across multiple sections.
- Updated user journeys to distinguish explicit bundle-path ingest from depot publishing.
- Updated R2 spec wording to avoid using `sync` as an operation name; it now says refresh/ingest or pending-delta where appropriate.
- Removed the standalone depot model sketch and replaced it with CI-backed Go state-machine/property references.
- Added `aha verify` / `verify --repair-fts` to README, generated command docs, user journeys, and agent-facing TODO guidance.
- Updated stale schema references to current bundle schema `agent-session-snapshot-bundle/v2` while keeping depot key/catalog layout `v1` terminology distinct.
- Added `docs/performance-audit.md` and linked it from README/TODO.

## Intentional historical references

- `CHANGELOG.md` mentions removed pre-release features (`--out`, `bundle_out_dir`, `.receipt.json`) because changelogs should record removals.
- `docs/r2-snapshot-aggregation-spec.md` mentions old `bundle_out_dir`/`--out` only in the pre-release cutoff/removal rationale.
- `docs/agent-history-aggregator-spec.md` still contains general no-live-sync context because that remains a non-goal.

## 2026-05-25 performance-doc refresh

A fresh consistency audit after the performance/profiling work found and fixed these doc drifts:

- `docs/performance-results.md` now includes the required `AHA_PATHOLOGICAL_INGEST_LARGE` / `AHA_PATHOLOGICAL_INGEST_XL` env vars for 50k/100k ingest benchmark commands.
- `docs/performance-scalability-plan.md` now labels the first pathological run as a historical baseline, aligns latest benchmark numbers with `docs/performance-results.md`, and describes rowid FTS verification as completed rather than pending.
- `internal/cli` command metadata and generated `docs/commands.md` now describe `refresh` as snapshot-or-reuse plus pending/new ingest, and `status` JSON includes path-token tables plus depot listed/unique/fetch counters.
- `README.md` now matches no-op refresh behavior by documenting unchanged depot-bundle reuse via `state_sha256` metadata.
- `docs/performance-audit.md` now distinguishes original audit date from the latest update date.
- `docs/lessons-learned.md` now records the profiling lessons from the latest ingest/search/verify pprof run.
- `docs/refactor-metrics-and-go-audit.md` captures the duplication-refactor before/after metrics, profiling artifacts, regression verification, and Go best-practices audit.

## Validation

```bash
git diff --check
go test ./...
scripts/verify.sh full
scripts/verify.sh mutation-dry
```
