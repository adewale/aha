# Documentation Consistency Audit

Date: 2026-05-19

Scope: README, changelog, command docs, trust docs, user journeys, architecture, R2 snapshot aggregation spec, and the original aggregator spec.

## Checks performed

- Current CLI nouns are consistent: **bundle**, **depot**, **catalog**, **corpus**, **ref**.
- Removed pre-release `--out` / `bundle_out_dir` from current user-facing docs and config examples.
- Removed local receipt sidecar requirements from current architecture/trust/spec text; `CHANGELOG.md` documents the removal.
- Verified `docs/commands.md` remains generated from command metadata.
- Consolidated architecture explanation so command flows and aggregation/deduplication are described in one place instead of repeated across multiple sections.
- Updated user journeys to distinguish explicit bundle-path ingest from depot publishing.
- Updated R2 spec wording to avoid using `sync` as an operation name; it now says refresh/ingest or pending-delta where appropriate.

## Intentional historical references

- `CHANGELOG.md` mentions removed pre-release features (`--out`, `bundle_out_dir`, `.receipt.json`) because changelogs should record removals.
- `docs/r2-snapshot-aggregation-spec.md` mentions old `bundle_out_dir`/`--out` only in the compatibility/removal rationale.
- `docs/agent-history-aggregator-spec.md` still contains general no-live-sync context because that remains a non-goal.

## Validation

```bash
git diff --check
go test ./...
```
