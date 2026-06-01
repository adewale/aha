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
- Final reviewed implementation: current `main` after the latest audit/fix pass.
- Final review result: requires rerunning the audit checklist after any later feature change; do not rely on old clean-review commit IDs.
- Post-audit hardening result: subcommand help, image exclusion, symlink/path hygiene, bounded ingest, conflict/error handling, and trust-doc verification were fixed with regression coverage before revalidating.

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

- The bundle is the durable evidence; the corpus is the query index. Ingest must trust bundled bytes over mutable live paths.
- V1 should preserve raw source data even when normalization is incomplete.
- Artifact data is part of agent history, not ancillary output. Text artifacts need full indexing; image artifacts need blob preservation; unlinked artifacts need readable identities.
- Search/read coherence is a product invariant: every hit must be passable to `aha read`.
- Depot behavior is clearest as set and round-trip properties: `Fetch(Put(x)) == x`, duplicate `Put(x)` is not new work, pending ingest is `catalog - corpus`, and catalog merge preserves one ref per bundle SHA.
- Sidecar metadata is a product surface. Removing local receipt sidecars simplified the depot model: bundle bytes, embedded manifests, catalog refs, and command JSON are enough.
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

## Post-audit hardening lessons

- `--help` is behavior, not incidental flag-parser output; every subcommand help path should exit successfully.
- Privacy flags must affect all image preservation paths, including file artifacts, not only embedded prompt images or the `images` table.
- Skipped data still needs integrity validation. If an image artifact is ignored because `include_images=false`, ingest must still hash/read the tar entry before accepting the bundle.
- Symlink safety needs defense in depth: discovery skips symlinks, snapshot copy rejects non-regular files, and output/repo paths are checked both lexically and through existing symlink resolution.
- Archive validation must bound compressed size, manifest size, file count, per-entry size, and total declared uncompressed bytes before spooling untrusted bundle content.
- Trust-doc commands are contracts; test names and docs must stay synchronized.

## Correctness-by-construction preparation lessons

- Prior art agrees with the `aha` architecture: immutable raw evidence first, normalized records second, derived indexes third, stable refs and verification on top.
- “Correct by construction” in Go/SQLite must be honest: some states can be made unconstructible, while others can only be encapsulated, statically guarded, detected, and repaired.
- Phase-0 guardrails are not busywork. Static debt inventories, state-machine skeletons, adapter conformance fixtures, schema introspection helpers, named seams, verifier queries, and mutation dry-runs make later refactors smaller and safer.
- Verification should be one workflow, not a wiki list of commands. CI and local development need the same scriptable profiles for quick checks, full checks, fuzzing, and mutation dry-runs.
- Mutation dry-runs are useful before trusting a new test net: they show uncovered critical code before a refactor removes duplicate runtime checks.
- Because `aha` is pre-user/pre-release, the CbC phases should delete transitional compatibility bridges: emit and parse canonical refs only, use v2 session keys only, and reject unsupported bundle schemas instead of carrying aliases.
- SQLite construction is strongest when paired with repair: targeted `CHECK`/`FOREIGN KEY` constraints and triggers block true invalid states, while verifier/reconciler queries remain necessary for direct SQL drift and recovery.
- Sealed canonical refs paid off more than compatibility shims. Removing optional ref DTOs and old ref syntax made search/read contracts simpler and made malformed states easier to reject at the boundary.
- Verification needs to be user-facing, not only test-facing. `aha verify --json` and `aha verify --repair-fts` turn corpus drift detection into an operational recovery path for humans and agents.
- Lightweight formal sketches are not useful unless they are executable or checked. Prefer Go state machines/properties already run by CI over standalone model notes that can drift.
- Open-world agent data should not get strict enum `CHECK` constraints too early. Typed role helpers can centralize decisions without rejecting future roles from raw histories.

## Agent-facing surface lessons (MCP, dashboard, code-mode)

- Do not own the wire format. The first MCP server hand-rolled JSON-RPC framing and inherited tracebase's `Content-Length:` framing, which real MCP clients cannot decode (the spec mandates newline-delimited JSON). Migrating to the official `github.com/modelcontextprotocol/go-sdk` deleted the framer/protocol/fuzz code (~350 lines) and made spec-version drift, structured output, tool annotations, and lifecycle someone else's problem.
- Cross-SDK conformance finds what spec-reading and self-testing cannot. Testing a framer against itself proves nothing. Driving `aha mcp` from three independent SDK clients (Python, TypeScript, Go) and driving our TS client against three SDK servers — six legs in both directions — surfaced bugs invisible to one implementation: empty Go slices marshalling to `null` instead of `[]`, and the Python SDK's Pydantic model rejecting array `structuredContent` as a type error. A bug visible to only one SDK is still a real bug.
- Read what the library actually does before writing a workaround for it. Two helpers (`rejectExtras`, `objectResult`) existed to enforce `additionalProperties:false` and to build dual text+structured results — both of which the SDK already does on every call (`applySchema` validation; auto-fill of content and structuredContent from a non-nil typed return). Tracing the SDK source deleted both helpers with no behaviour change.
- A generated client surface needs a drift test or it rots silently. `clients/typescript/aha-mcp.ts` is reflected from the Go types; a byte-comparison test regenerates it in CI and fails when the checked-in file is stale, so the typed surface cannot diverge from the server it describes.
- One dispatch, many transports. CLI, MCP (stdio), and the HTTP dashboard all route through a single `mcp.CallTool`. An in-process consistency test sends the same tool+args through both the HTTP and MCP paths against one backend and asserts equal payloads, so a change to one skin cannot silently diverge from the others.
- Read-only is strongest by construction, not by flag. The MCP server simply never registers write tools; there is no "disable writes" toggle to misconfigure. An agent cannot reach `ingest`/`snapshot`/`refresh` over MCP because those handlers were never added.
- Dashboard security is defense-in-depth and fail-closed. Loopback bind by default; non-loopback refused at `Listen` time unless `--allow-remote` and a bearer token are both set; Host-header allowlist with numeric-port enforcement (blunts DNS-rebinding); JSON content-type enforcement on POST; strict CSP. Each layer landed with a hostile-input regression test (IMDS hostnames, IDN homographs, malformed IPv6 brackets).
- Position honestly: substrate versus product. The README originally promised pattern detection ("spot patterns, turn them into skills"). This branch ships the substrate that makes that cheap, not the detection itself. Walking the claim back to "examine your behaviour today; pattern detection is the next layer" keeps the front door truthful.
- CI is a contract, and a silent shell bug makes it a lie. A global bash `RETURN` trap referencing a function-local `$tmpdir` re-fired when an outer function returned and tripped `set -u`, so `verify.sh ci` had been failing since the conformance suite landed while `verify.sh mcp` passed. Mode-specific green is not whole-suite green; run the exact mode CI runs before claiming the build is fixed.

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

## Performance testing lessons

- Pathological does not always mean large. Years of trivial bundles, duplicate catalog refs, or repeated no-op refreshes can be a worse scalability shape than one huge archive.
- Test performance claims at the cheapest layer that can falsify them: pure/model PBT for cardinality and idempotence, fake-driver counters for network/fetch/byte-read claims, tiny SQLite query-plan tests for indexing claims, package benchmarks for constants, and CLI pprof only for end-to-end confirmation.
- Prefer deterministic performance invariants over wall-clock assertions in unit tests. Assert unique work units, fetch counts, bytes read, output cardinality, query plans, and idempotent state transitions.
- Benchmarks and pprof answer different questions than PBT. Benchmarks show cost; profiles show where cost lands; PBT says what must not grow with duplicates, stale refs, old trivial bundles, or catalog ordering.
- Package-level profiling is usually cheaper and clearer than command-level profiling. Keep CLI pprof opt-in for real command journeys, but optimize from the smallest benchmark that reproduces the issue.
- Be precise in docs about complexity: deduping output by unique SHA does not mean the implementation avoids scanning raw catalog rows. Distinguish metadata scanned from work performed.
- A performance plan is incomplete without user-journey metrics. Each abstraction change needs a baseline, a scenario that should become measurably better, and a counter/benchmark/profile that proves the improvement happened.
- Profile enough iterations to separate setup from the target path. The latest ingest/search/verify profiles were useful because ingest ran one ~1s operation, search ran 20 broad queries, and verify ran 100 checks; the allocation profiles still exposed benchmark setup, pprof startup, and package init noise that should not be mistaken for product hot paths.
- When CPU lands mostly in SQLite and syscalls, prefer semantic changes over Go micro-optimizations. The latest ingest profile put nearly all CPU under SQLite statement execution and `pwrite`, so true multi-row inserts remain a possible constant-factor change, not an obvious correctness-preserving rewrite to do without stronger evidence.
- Search profiles and query-plan tests answer different questions. Query-plan tests prove the indexed project/path-token filters are used; pprof showed broad common-term searches are still SQLite/FTS candidate work, so output caps reduce memory/user cost without promising broad-term ranking speedups.
- Verify profiles can validate that a repaired algorithmic cliff stays repaired. Rowid-backed FTS verification now profiles as small SQLite count work rather than the former seconds-scale join path; extra stats counters are acceptable only because the benchmark remains millisecond-scale.
- Refactor-only work still needs before/after metrics and a behavior audit. The duplication pass found that moving shared FTS predicates was not enough; schema triggers and migrations had to use the same expression or existing corpora would keep stale behavior.
- Atomic write helpers must state their race semantics. “Existing OK” is not the same as no-replace unless the final publish step is atomic; returning whether this process actually created the file prevents misleading depot reports.

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
- Pattern detection over the corpus is documented (`docs/research/agent-trace-tools.md`) but not built; this branch ships only the substrate that makes it cheap.
- The HTTP transport throws plain `Error("<code>: <message>")` while the stdio client throws a typed `AhaMcpError`; symmetric typed errors across both transports are deferred.
- There is no batch-read MCP primitive; code-mode callers fan out with `Promise.all(refs.map(read))` instead, which already collapses to one model turn.
