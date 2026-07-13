# Correctness-by-construction improvement spec

This spec captures the follow-up work suggested by comparing `aha` with prior art: Rust typestate/newtypes, SQL invariant engines, content-addressed stores, property/state testing, simulation testing, mutation testing, and static analysis.

## Scope

`aha` remains a pragmatic Go/SQLite CLI. The goal is not formal proof; the goal is to make the most important states unconstructible, statically guarded, or verifier-repairable.

## Required improvements

| Area | Requirement | Implementation hook |
|---|---|---|
| Sealed refs | Search/read identity must use closed canonical variants, not optional string DTOs. | `model.Ref`, `MessageRef`, `SessionRef`, `ArtifactRef`; canonical `FormatRef`/`ParseRef`; JSON marshaling on variants. |
| Typed errors | User-visible fallible resolution must expose typed errors before string classification. | `corpus.NotFoundError`, `corpus.AmbiguousError`, `archive.UnsupportedSchemaError`, CLI JSON error classification. |
| Corpus verify/repair | Users and agents need direct corpus verification and FTS repair, not only `doctor`. | `aha workspace verify [--repair-fts] [--json]`. |
| Storage invariants | True row relations should be represented in SQLite where they do not block quarantine/provenance. | Targeted FK/CHECK constraints plus append-only/conflict triggers. |
| FTS drift | FTS remains derived from raw corpus rows and must be repairable. | Trigger-maintained FTS for normal writes; `corpus.Verify`; `corpus.ReconcileFTS`; `aha workspace verify --repair-fts`. |
| State/failure tests | Operation sequences and crash windows must be tested with real files/SQLite/depot stores. | Existing rapid state machines plus ingest failure-injection hooks; keep expanding sequences. |
| Static analysis | Correctness debt must fail tests when it grows. | AST/static tests for time, FTS writes, append-only mutation, identity construction, network imports. |
| Canonical bundle fixture | Archive drift needs a small deterministic golden beyond repeat equality. | `TestCanonicalBundleGoldenSHA`. |
| End-to-end journeys | CLI workflows must cover search→read, bad refs, verify/repair, depot ingest, and JSON contracts. | CLI journey tests and generated `docs/commands.md`. |
| Performance risk tracking | Integrity-heavy paths must have explicit hotspot documentation and benchmark plans before optimisation. | `docs/performance-audit.md`. |

## Deliberate non-goals

- No compatibility migration for pre-release refs/session keys/bundle schemas.
- No client-side encryption or remote corpus database.
- No claim that arbitrary direct SQL cannot create drift. Direct drift must be guarded by static tests and repaired by verifier/reconciler.

## Decision rules

1. If a relation is always true and does not prevent quarantine/provenance, enforce it in SQLite.
2. If a relation can be absent because snapshots are scoped or evidence is partial, store it as provenance without an FK.
3. If a derived index can drift through direct SQL, make app writes correct and provide verifier/repair.
4. If a public string crosses a boundary, parse into an opaque type immediately.
5. If a decision is pre-release compatibility only, delete it and add a rejection test.

## Validation required

Before considering this complete, run:

```bash
scripts/verify.sh full
scripts/verify.sh mutation-dry
```

The mutation dry-run is an inventory gate: uncovered mutants become follow-up work unless they are outside the changed invariant-critical path.
