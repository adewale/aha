# AHA Lessons Learned

This document captures the implementation lessons from the Agent History Aggregator (`aha`) rollback/reimplementation cycles. It is intentionally blunt: these are the things that changed the product, tests, architecture, or process.

## Cycle ledger summary

| Cycle | Main lesson | Outcome |
|---:|---|---|
| 1 | A monolithic CLI can prove feasibility but is not a v1 architecture. | Rolled back and replaced with packages. |
| 2 | Package boundaries make review and hardening possible. | `cmd/aha` plus `internal/*` architecture established. |
| 3 | Archive/ingest/blob lifecycle safety is core, not polish. | Streaming validation, staged promotion, atomic blob writes. |
| 4 | Docs, tests, and implementation must agree exactly. | Fixed post-query flags, summary indexing, full artifact FTS, image artifacts, Claude subagent fixture. |
| 5 | Every search result must be readable, and CLI escaping must preserve literal queries. | Full artifact read body and `--` terminator semantics. |
| 6 | Schema evolution needs migrations from the first release. | Added idempotent migration and old-schema test. |
| 7 | Spec hygiene is part of correctness. | Cleaned stale remaining issues and added cycle accounting. |
| 8 | Ingest must be bundle-pure. | Pi identity now comes from bundled bytes, not mutable live paths. |
| 9 | Release readiness needs first-class lessons, classified open questions, and CI/release hardening. | Added this document and tightened spec classification; review caught stale spec cycle counts. |
| 10 | Process accounting needs regression tests too. | Added doc-sync coverage for current cycle/attempt/rollback counts. |

Current counts after cycle 10:

- Implementation attempts built: 7.
- Full implementation rollbacks committed: 6.
- Lesson/spec-update cycles recorded: 10.
- Final reviewed implementation: `083cf5d` plus ledger/doc fix `aded92f`.
- Final review result: clean enough for v1; no rollback-worthy P0/P1 regrets.

## Final v1 status after ten cycles

- V1 locked decisions and definition-of-done items are implemented and covered by tests/review evidence.
- V1-blocking open questions: 0.
- Post-v1 release-hardening tasks: large-corpus performance validation, more anonymized real Claude fixtures, and release notes.
- V2/later items: redaction/public dataset prep, Windows support, configurable project grouping, opt-in tool-output indexing, source-native branch/thread reads, conflict UX refinements, and OCR/captioning.
- Accepted residual regrets are documented below; none are P0/P1 for v1.

## Claude History Explorer comparison lessons

- CHE has no `LESSONS_LEARNED.md` in the checkout inspected; its `TRUST.md`, architecture docs, parser/project code, and tests serve as the practical lessons corpus.
- CHE's strongest reusable lesson is verifiable trust: state read-only/no-network claims and back them with static tests and user-verifiable commands.
- CHE's parser confirms the importance of line-by-line JSONL parsing, malformed-line tolerance, `agent-*` subagent recognition, and careful treatment of `tool_result` blocks.
- AHA intentionally differs from CHE by snapshotting immutable multi-source bundles and ingesting into SQLite rather than searching one live Claude tree on demand.

## Product lessons

- The bundle is the receipt; the corpus is the index. Ingest must trust bundled bytes over mutable live paths.
- V1 should preserve raw source data even when normalization is incomplete.
- Artifact data is part of agent history, not ancillary output. Text artifacts need full indexing; image artifacts need blob preservation; unlinked artifacts need readable identities.
- Search/read coherence is a product invariant: every hit must be passable to `aha read`.
- Tool output is preserved but not indexed in v1. This must be enforced in ingest, not left as documentation.
- Image prompt reconstruction needs occurrence metadata: content index, prompt order, raw reference, mime type, and blob hash.
- Windows support is a v2 concern; keeping Windows-shaped fixtures is still useful to avoid v1 assumptions that would block v2.

## Architecture lessons

- Start with a small architecture skeleton before command breadth: model, adapters, archive, corpus, search, CLI, tests.
- Keep source-specific logic inside adapters. Archive, ingest, search, and read should be source-agnostic.
- SQLite is the right v1 corpus engine. Use FTS5, uniqueness, indexes, transactions, constraints, and migrations rather than reimplementing database behavior in Go.
- Determinism requires deliberate design: stable discovery order, canonical manifest output, normalized tar metadata, deterministic compression, and pinned test metadata.
- Content-addressed blobs must be written with temp-file plus atomic rename and never overwritten in place.
- Rejected or corrupt bundles must not be promoted into the corpus store.
- Schema changes require idempotent migrations and tests that simulate old corpus shapes.

## Testing lessons

Invariant tests should come before feature breadth. The non-negotiable invariants are:

- immutable ingest identity;
- deterministic archive bytes;
- search/read coherence;
- artifact preservation and unlinked artifact reads;
- schema migration from older shapes;
- no default tool-output indexing;
- conflict quarantine/no overwrite;
- parser robustness/fuzz safety.

Additional testing lessons:

- Use real temp files, tar/zstd archives, and SQLite databases; avoid mocks for core storage behavior.
- Every review-discovered bug should get a regression test before the fix is considered done.
- Synthetic fixtures are useful but insufficient. Add anonymized real-world-shaped Pi and Claude Code fixtures early.
- Real-history smoke tests are necessary before release: they caught that real Pi entries use top-level `type:"message"` plus nested `message.role`, while early fixtures had top-level roles.
- Tests should assert specific fields and negative cases, not just non-empty output.
- Documentation examples are tests: if README shows `aha search query --json`, the CLI must support that form.

## Documentation lessons

- The README is the front door: answer what it is, why it matters, and how to use it within the first screen.
- Show the shortest real path first; move flags and edge cases to journey docs.
- Keep prose specific. Avoid generic AI-doc filler such as “powerful,” “seamless,” “robust,” and “unlock.”
- Put rationale near defaults so users can predict behavior instead of memorizing flags.
- Link to deep docs; do not duplicate the full spec in the README.

## Process lessons

- The loop must be literal: update spec → implement → review → update spec with lessons → repeat.
- Do not claim v1 completion from passing tests alone; require fresh adversarial review.
- Keep cycle accounting from day one: implementation attempts, rollbacks, lesson commits, and stop reasons.
- Add doc-sync tests for process/accounting invariants once the spec depends on them.
- Add static trust tests for claims users are asked to rely on, especially no source-history mutation and no network behavior.
- Avoid commits that mix unrelated docs/spec/code changes.
- Open questions must be classified as locked decisions, explicit v2/post-v1 items, or true blockers.
- Treat “remaining issues” as release-blocking unless explicitly marked as v2/post-v1/non-goal.

## Regrets fixed during the cycles

- Monolithic first implementation.
- Filename-derived Pi session identity.
- Missing unlinked artifact read path.
- Preview-only artifact indexing.
- Artifact hits that could not be read coherently.
- Tool output policy stated but not enforced.
- Non-atomic blob writes.
- Whole-bundle memory assumptions.
- Corrupt bundle promotion risk.
- Missing generic summary indexing.
- Missing non-embedded image artifact blobs.
- Missing Claude `agent-*.jsonl` fixture.
- Documented CLI forms that did not work.
- Broken `--` literal query semantics.
- Missing existing-corpus migration for `artifacts.text_body`.
- Mutable live-path reads during bundle ingest.
- Stale open questions in the spec.
- Stale implementation-cycle counts after the ninth redo.
- Synthetic Pi fixtures initially masked real Pi `message.role` parsing, causing real Pi histories to ingest entries without indexing user/assistant messages.

## Regrets intentionally accepted or deferred

- V1 does not redact secrets; bundles and corpora are private.
- Windows support is v2.
- Project identity is heuristic in v1.
- `read` uses file-order context rather than full source-native branch reconstruction.
- Tool-output indexing is not opt-in yet.
- Conflict display/search policy can be improved later.
- More real anonymized Claude Code image/attachment fixtures are still needed before a broader release claim.
- Performance validation on very large real corpora remains release-hardening work.
