---
title: Correctness by Construction Refactor Spec
kind: spec
created: 2026-05-24
updated: 2026-05-24
tags:
  - aha
  - correctness-by-construction
  - property-based-testing
  - state-machine-testing
  - fuzzing
  - types
  - sqlite
  - go
  - specification
source_type: design
status: implementation-prep
aliases:
  - CbC refactor
  - aha correctness spec
---

# Correctness by Construction Refactor Spec

## Purpose

`aha` already has unusually strong tests for a young local-history tool:
archive hardening, parser fuzzing, SQLite integration tests, depot contracts,
R2 fake-S3 tests, generated command docs, golden renderers, property-style
checks, and race coverage. The remaining correctness risk is not lack of tests;
it is that the most important invariants still live in **string conventions,
imperative sequencing, and duplicated checks**.

This spec defines a refactor that moves those invariants toward construction:

- the invalid value cannot be constructed;
- the invalid row cannot be inserted;
- the invalid operation is not exposed by the interface;
- the invalid sequence is rejected by a model/state-machine test;
- where Go/SQLite cannot make it impossible, the residual risk is explicitly
  tested and documented.

This is not a rewrite. It is a staged hardening plan that preserves behavior
until the identity-format migration phase.

## Non-goals

- No new adapters, semantic search, redaction, MCP, OCR, or product features.
- No move away from SQLite/FTS5, `modernc.org/sqlite`, `tar.zst`, JSONC config,
  or stdlib `flag`.
- No attempt to type or normalize every raw input field. Agent JSONL is
  open-world and evolving; raw bytes stay lossless.
- No claim of formal proof. In Go, “correct by construction” means opaque types,
  smart constructors, closed internal variants, DB constraints, repository
  encapsulation, static guards, and model/property/fuzz tests.

## Current-state accuracy baseline

The plan below is grounded in the current code shape:

- `internal/model/ref.go` now uses sealed canonical ref variants (`MessageRef`,
  `SessionRef`, `ArtifactRef`) with one versioned string codec.
- `internal/corpus/ingest.go` constructs v2 `sk1_...` session keys through the
  typed constructor and relies on trigger-maintained FTS for normal writes.
- `internal/corpus/read.go` separates exact canonical reads from fallible fuzzy
  human resolution.
- `internal/corpus/schema.go` uses primary/unique keys, targeted foreign keys,
  checks, append-only/conflict triggers, and trigger-maintained FTS.
- malformed JSONL lines currently become diagnostics while the **source file**
  is preserved as a blob; there is no per-line malformed-entry row.
- deterministic bundle coverage includes repeat/shuffle byte equality and a
  committed canonical bundle SHA fixture.
- fuzz targets exist for parser, refs, archive, and depot address/key parsing;
  `rapid` is now available for shrinkable PBT/stateful testing.

When this spec says “target,” it means proposed construction, not current
behavior.

## Principles

1. **Make illegal states unrepresentable where Go/SQLite can.** Prefer a type,
   constructor, constraint, trigger, or capability over a comment or repeated
   runtime check.
2. **Parse once at the boundary.** Untyped JSON, paths, refs, depot addresses,
   and config values cross into typed values exactly once.
3. **Preserve raw input.** Strong typing must never turn unknown agent data into
   data loss. Unknown input yields raw bytes plus diagnostics.
4. **Separate canonical APIs from human convenience.** Canonical refs and keys
   should be total. Fuzzy/prefix/human resolution should be explicitly fallible.
5. **Put storage invariants in storage.** If a corpus row relation is invariant,
   encode it as a `PRIMARY KEY`, `UNIQUE`, `CHECK`, `FOREIGN KEY`, or trigger.
6. **Do not overclaim.** SHA-256 identity is collision-resistant, not
   mathematically injective. FTS triggers synchronize normal writes; direct FTS
   table writes remain possible unless repository/static guards block them.
7. **Defense in depth only for different adversaries.** Do not repeat the same
   internal check across layers; keep multiple layers when they defend different
   risks: hostile input, recovery, external storage, or forensic repair.
8. **Every invariant needs a mechanism and an oracle.** Mechanism: type/schema/
   capability/trigger. Oracle: example, property, fuzz, state machine,
   exhaustive, mutation, or static guard.

## Invariant inventory

| Invariant | Current enforcement | Target construction | Verification |
|---|---|---|---|
| Local-only by default | docs + static no-network test | network imports only in `internal/depot`; R2 only via explicit depot address/config | static import guard + CLI JSON contract tests |
| R2 identifiers are valid before networking | opaque `R2Bucket`/`R2AccountID`; `ParseAddress` and resolved config reject malformed and placeholder values | invalid bucket/account/credential/endpoint values cannot construct an R2 client | constructor tables + instrumented zero-request CLI test + live contract test |
| Credentials never persist | resolved secret-bearing R2 config has private fields and no JSON representation; persisted config accepts only non-secret account/endpoint/region fields | secret-bearing type never serializes; config writer cannot accept secret fields | config round-trip + redaction/non-leak tests |
| Legacy corpus rebuild always preserves recoverability | exclusive corpus lifecycle capability; verified sibling staging at the final backup path; one atomic directory exchange; no no-backup CLI variant | callers cannot request destructive rebuild, promotion cannot occur before replacement verification, and the configured root is never absent | build/verification/swap-failure, overlap, lock-exclusion, collision, backup-preservation, and CLI journey tests |
| Source reads are read-only | adapter convention + static grep | adapter/source capability exposes read/discover/open only; no write authority | compile-time interface + static mutation guard |
| Source path safety | path containment checks | `SafePath`/`SourceRoot` constructed once; write destinations require corpus/depot roots | PBT path containment + Type B unsafe path tests |
| Parser never drops raw source bytes | source file blob stored before parse | raw file always stored; optional malformed-line diagnostics table for per-line raw | fuzz postconditions + fixture conformance |
| Parsed entry identity non-empty | `EntryID` constructor + DB trigger/check | `EntryID` constructor cannot return empty; zero value invalid at DB boundary | constructor PBT + DB `CHECK(entry_id <> '')` |
| Session identity stable | `SessionKey` constructor emits `sk1_` hash from length-prefixed tuple; DB rejects non-`sk1` keys | `SessionKey` from canonical length-prefixed tuple; tuple columns retained for audit | deterministic/collision-class PBT + schema Type B tests |
| Identity collision handling | primary keys + conflict branch | collision-resistant key plus tuple uniqueness; hash collision becomes explicit conflict | Type B same-key/different-tuple attempt |
| Refs round-trip | sealed `Ref` variants + canonical parser | sealed ref variants: `MessageRef`, `SessionRef`, `ArtifactRef`; one versioned string codec | rapid PBT + fuzz + search/read property |
| Canonical read is total | `read` resolver may be ambiguous | `ReadCanonical(MessageRef|ArtifactRef)` exact lookup; `ResolveHuman` separate | state-machine: every emitted ref readable |
| Human resolution is honest | ambiguous errors at runtime | fallible resolver returns typed `NotFound`/`Ambiguous` with candidates | table tests + PBT prefix escaping |
| Bundle determinism | repeat/shuffle byte equality tests | canonical archive/manifest encoder with sorted input and pinned metadata | PBT shuffle invariance + committed golden fixture |
| Bundle honesty | archive validation/budgets | manifest/data/checksum validation in one typed reader | fuzz corruptions + Type B malformed bundle tests |
| Bundle identity | content SHA | `BundleRef` always content-addressed; key validates SHA | depot contract + fuzz key/address tests |
| Depot object truth | bundle object SHA + catalog refs | bundle objects are durable truth; catalog shards are repairable indexes | local/fake-R2 contract + repair state machine |
| Catalog merge laws | merge helper + property tests | shard merge is idempotent, commutative, deterministic | rapid/quick algebra tests + concurrent fake-R2 tests |
| Corpus ingest idempotent | bundle PK/unique + explicit skip + `insert or ignore` | bundle identity and row identity encoded as uniqueness; attempt log append-only | state-machine `ingest∘ingest = ingest` |
| Entries append-only | `BEFORE UPDATE/DELETE` triggers reject mutation on append-only tables | `BEFORE UPDATE/DELETE` triggers reject mutation on append-only tables | Type B direct SQL update/delete tests |
| Same `(session,entry)` diff hash quarantined | select-then-branch in Go | `BEFORE INSERT` trigger or writable-view trigger writes `conflicts` and ignores overwrite | Type B conflict insertion + state machine |
| Cross-machine conflicts preserved | Go query/branch | unique source tuple + conflict relation; no overwrite path | multi-machine model tests |
| Message rows belong to entries | FK plus `messages_require_entry` trigger | FK `messages(session_key,entry_id) → entries` | Type B orphan insert rejected |
| Asset rows belong to entries/images | FK from `entry_assets` to `entries`; image hash is optional for raw refs | FKs from `entry_assets` to `entries`; image hash relation checked where possible | Type B orphan asset tests |
| Artifact rows are searchable/readable | artifact row + FTS trigger/reconciler; artifact ref codec | artifact row + FTS trigger/reconciler; artifact ref codec | search/read property includes artifacts |
| FTS cannot drift through normal writes | trigger-maintained FTS rows | trigger-maintained FTS rows plus verifier/reconciler; raw tables remain truth | state machine + reconciliation query |
| FTS direct writes are contained | not structurally blocked | repository encapsulation + static guard banning direct FTS writes outside schema/reconciler | static guard + Type B reconciliation test |
| Blob rows match files | staging + hashes + rollback tests | blob publisher owns atomic write; corpus verifier checks row↔blob↔hash | failure-injection tests + `doctor`/verify tests |
| Orphan blobs are benign/repairable | some cleanup on bundle promote failure | explicit orphan policy: allowed staging or repairable orphan, never missing committed blob | state-machine + repair tests |
| Status counts exact | integration tests | status reads from constrained schema/views | property/state-machine count agreement |
| JSON CLI contracts stable | generated docs + tests | registry metadata and renderers are source of truth | docs sync + golden + JSON envelope tests |
| Time is explicit | multiple `time.Now`; bounded conditional-write retry uses a context-cancellable timer with capped jitter | `Clock` and `Sleeper/Backoff` capabilities at production seams | cancellation + bounded-contention tests; static debt guard then forbidigo/static ban |
| Onboarding sequence is valid | doctor/setup derive one action from depot/corpus/config state; structured argv preserves config overrides | invalid transitions are absent from the closed action mapping | state-table tests + exact-one-next JSON contract |
| Progress cannot corrupt output or leak identity | closed phase/kind/unit enums and aggregate-only events; renderers receive no paths, IDs, endpoints, keys, errors, or credentials | core operations cannot put sensitive strings into progress events; stdout is never a progress sink | renderer concurrency/privacy tests + final-JSON/NDJSON contracts + race detector |
| Causal errors cannot become public output | opaque `usererror.View` and `Action` values are created only by typed normalization; raw causes remain behind `Unwrap` | CLI, MCP, HTTP, and smoke-test boundaries render one safe message and one constructed action; verbose diagnostics are allowlisted fields, never `err.Error()` | every-command JSON contract + boundary static test + secret/path/SQL canaries + `errors.Is` preservation |
| Smoke tests cannot inherit production capabilities or destinations | R2 integration accepts opaque credentials through `ResolveR2ConfigExplicit`, which never reads environment; wrapper recognizes only `AHA_R2_SMOKETEST_*` plus explicit target flags | production credential names are rejected as fallback, matching test/production keys fail locally, and the child environment has production providers removed; local smoke workspaces come only from fresh `mktemp -d` roots | production-only/matching-key/fake-child environment tests + static integration-source guard + live namespaced cleanup test |
| No new correctness debt | code review convention | debt inventory tests fail if raw identity/time/FTS patterns spread | `internal/testquality` static inventory |

## Construction design

### 1. Typed boundary values

Introduce opaque values in `internal/model` or focused subpackages:

- `SourceName`
- `MachineID`
- `SourceSessionID`
- `SessionKey`
- `EntryID`
- `BundleSHA256`
- `ArtifactSHA256`
- `Role`, `EntryKind`, `AssetKind`, `CopyState`, `RefKind`
- `CorpusRoot`, `DepotAddress`, `SafeRelPath`

Each has one public constructor/parse function. The zero value is invalid; every
DB boundary and formatter checks `Valid()` or stores only constructed values.
This is an explicit Go limitation, not a proof hole to ignore.

### 2. Canonical identity without delimiter fragility

Current session keys are delimiter strings. The target identity is:

```text
SessionKey = "sk1_" + hex(sha256(length_prefixed(source, machine_id, source_session_id)))
```

Retain tuple columns (`source_name`, `machine_id`, `source_session_id`) with a
`UNIQUE` constraint. This gives:

- compact stable keys for refs;
- no `:`/`#` delimiter ambiguity;
- provenance/audit columns for humans;
- a DB place to detect the practically impossible hash-collision case.

PBT can prove deterministic canonical encoding and cover delimiter/pathological
cases. It cannot prove SHA-256 injectivity; the spec relies on collision
resistance plus tuple uniqueness.

### 3. Ref sum type and versioned codec

Use sealed variants for refs:

```go
type Ref interface{ refVariant(); Valid() bool }
type MessageRef struct { Session SessionKey; Entry EntryID }
type SessionRef struct { Session SessionKey }
type ArtifactRef struct { SHA ArtifactSHA256 }
```

The string codec should be versioned and delimiter-safe, e.g.:

```text
msg:v1:<base64url-session-key>:<base64url-entry-id>
session:v1:<base64url-session-key>
artifact:v1:<sha256>
```

Because `aha` is pre-user/pre-release, there is no legacy ref migration path:
the parser accepts only canonical refs, and the formatter emits only canonical
refs. `search` emits values accepted by `read` without fuzzy resolution.

### 4. Parser boundary and diagnostics

Keep the raw file/blob lossless. Add one typed parse boundary per adapter:

```go
type ParsedLine struct {
    Entry       ParsedEntry // present only when valid
    Diagnostic  *ParseDiagnostic
    Raw         string
    LineNo      int
}
```

Malformed JSONL can then be represented explicitly instead of being only a
session-level string diagnostic. Whether malformed lines become searchable is a
product decision; whether they are preserved should not be.

Adapter conformance fixtures should be data-driven JSON cases:

```text
adapter input raw line/file → normalized entry/diagnostic
```

Run the same harness for Pi, Claude Code, Codex, and OpenCode.

### 5. Storage constraints and triggers

Use idempotent migrations and old-shape DB tests. Target constraints:

- `NOT NULL`/`CHECK` for non-empty keys, known enum values, boolean integers,
  SHA length/hex shape where practical.
- FKs:
  - `entries.session_key → sessions.session_key`
  - `messages(session_key,entry_id) → entries(session_key,entry_id)`
  - `session_versions.session_key → sessions.session_key`
  - `session_versions.file_sha256 → files.file_sha256`
  - `entry_assets(session_key,entry_id) → entries(session_key,entry_id)`
  - `artifacts.bundle_id → bundles.bundle_id`
- append-only triggers for `entries`, `messages`, `artifacts`, and conflict log
  tables where mutation would violate history.

Correct SQLite trigger shape for quarantine on a real table is **not** an
`INSTEAD OF` trigger. Use one of:

1. `BEFORE INSERT ON entries` trigger:
   - when same `(session_key,entry_id)` exists with same hash: `RAISE(IGNORE)`;
   - when same key exists with different hash: insert into `conflicts`, then
     `RAISE(IGNORE)`;
   - otherwise allow insert.
2. a writable view with `INSTEAD OF INSERT` trigger, and ensure all corpus writes
   go through the view, not the base table.

Option 1 is simpler and should be tried first.

### 6. FTS without overclaiming

Trigger-maintained FTS rows are the target for normal writes:

- `messages_ai` insert trigger adds message text;
- `artifacts_ai` insert trigger adds artifact text/body;
- append-only update/delete triggers reject mutation.

But SQLite virtual tables remain directly writable and cannot carry ordinary
foreign keys. Therefore construction is layered:

- schema triggers keep normal writes synchronized;
- repository code exposes no direct FTS write API;
- static tests ban `insert/update/delete fts_*` outside schema/reconciler files;
- a verifier/reconciler query detects and optionally repairs drift.

The invariant is not “orphan FTS rows are physically impossible under arbitrary
SQL.” It is “the application construction path cannot create drift, direct drift
is statically guarded, and drift is detectable/repairable.”

### 7. Blob and row consistency

Treat the filesystem as a second store with its own invariants:

- bundle/file/image blobs are content-addressed by SHA;
- DB rows must never point at missing or wrong-hash blobs after commit;
- orphan staging blobs are allowed only before commit or as repairable garbage;
- rollback after blob promotion must clean up promoted bundle blobs or mark them
  repairable.

Add a corpus verifier that checks:

```text
bundles.bundle_sha256 ↔ blobs/bundles/<sha>.tar.zst
files.file_sha256     ↔ blobs/files/<sha>.zst after decompress/hash
images.image_sha256   ↔ images blob bytes
artifacts             ↔ file/blob rows where applicable
fts rows              ↔ source rows
```

`doctor` can surface a fast subset; `aha verify` exposes corpus checks and
repairable FTS drift directly. Deeper blob hash verification can be added as a
future `verify --deep` mode.

### 8. Depot correctness

Depot invariants are part of correctness by construction, not an add-on
(depot v2; see docs/depot-v2-spec.md I1-I7):

- blob key is exactly `blobs/v2/<sha>.zst` and the SHA matches the
  uncompressed content (verified on write and on read);
- snapshot identity is the SHA-256 of the canonical manifest encoding;
  only canonical bytes decode, so one manifest has exactly one identity;
- publish ordering is typestate: blobs before manifest before pointer,
  receipts unforgeable outside the package;
- writers are bound to one machine namespace; foreign keys inexpressible;
- steady-state paths have no delete and no list primitives;
- local and R2 drivers satisfy the same contract;
- R2 conditional writes are retryable (pointer and machines index);
- credentials never appear in config/manifests/logs/JSON.

Keep fake-S3 contract tests and add state-machine sequences that interleave
`Put`, duplicate `Put`, stale catalog refs, `Verify`, and `Repair`.

### 9. Deterministic time and retry seams

Time enters in snapshot/archive metadata, ingest attempt rows, bundle metadata,
depot markers, and retry backoff. Introduce:

```go
type Clock interface { Now() time.Time }
type Sleeper interface { Sleep(time.Duration) }
type Backoff interface { Delay(attempt int) time.Duration }
```

Production constructs real implementations once. Tests pass fixed clocks and
no-sleep sleepers. Static debt inventory should first freeze current ambient
clock sites; after the refactor, replace it with a ban outside the production
clock package.

### 10. Capabilities, not raw authority

Adapters and snapshot code should receive capabilities with only the operations
they need:

- source discovery/open: read/list/stat only;
- corpus writer: write only under corpus root;
- depot driver: put/list/fetch/verify only;
- clock/backoff: explicit seam.

This does not eliminate all path safety tests. It narrows which code can even
attempt a dangerous operation.

## Verification strategy

### Verification layers

| Layer | Tooling | Purpose |
|---|---|---|
| Example/contract tests | Go `testing`, temp dirs, real SQLite, fake-S3 | lock public behavior and sad paths |
| Golden tests | committed expected output/bundles where reviewable | detect renderer/canonical byte drift |
| Property tests | `pgregory.net/rapid` for shrinkable PBT; existing `testing/quick` where sufficient | laws over arbitrary inputs |
| Stateful tests | `rapid.T.Repeat` / state-machine model | operation sequences and interleavings |
| Fuzz tests | Go native fuzzing | hostile parser/ref/archive/depot bytes |
| Exhaustive tests | table/enumeration/permutations | small finite spaces like enum × flag |
| Type B invalid-state tests | direct constructor/SQL attempts | prove the claimed rejecting mechanism exists |
| CLI journey contracts | future `testscript`/real CLI invocations | prove command behavior, exit codes, JSON contracts, and filesystem effects |
| Differential tests | old path vs new path during migrations | prove refactors preserve behavior or document intentional changes |
| Schema contract tests | SQLite introspection helpers + direct SQL attempts | prove constraints/triggers/FKs exist and reject bad states |
| Static guards | `internal/testquality`, `go vet`, future forbidigo/custom `go/analysis` | stop convention debt from spreading |
| Mutation testing | `gremlins` on critical packages | prove tests kill likely bugs |

### Type A and Type B tests

For every invariant:

- **Type A:** for generated/valid inputs, prove the postcondition.
- **Type B:** try to construct the state the model claims impossible and assert
  the mechanism rejects it.

Do not add Type B tests before the rejecting mechanism exists. Otherwise the
suite documents a fantasy model.

### Rapid generators

Shared shrinkable generators should live in `internal/testutil`:

- refs and ref components;
- parsed sessions/entries/assets;
- manifest files and bundles;
- depot bundle refs/catalog shards;
- operation sequences for corpus/depot state machines.

Generators should produce realistic values by default and expose knobs for
pathological cases: delimiters, empty/near-empty text, duplicate IDs, conflicting
hashes, malformed refs, weird paths, large-but-budgeted blobs.

### Stateful corpus model

Model state:

```text
seenBundles: set[bundleSHA]
sessions:    map[SessionKey]SessionModel
entries:     map[(SessionKey,EntryID)]EntryHash
messages:    set[(SessionKey,EntryID)]
artifacts:   map[ArtifactSHA]ArtifactModel
assets:      set[(SessionKey,EntryID,AssetSHA,contentIndex,promptOrder)]
conflicts:   multiset[Conflict]
blobs:       map[sha]BlobKind
fts:         expected message/artifact search rows
```

Commands:

- ingest new bundle;
- ingest duplicate bundle;
- ingest same session/entry/different hash;
- ingest cross-machine same source session/different hash;
- search query;
- read canonical ref;
- status;
- simulate interrupted ingest hook;
- verify/repair corpus or depot.

After every command:

- row counts match the model;
- entries/messages/artifacts are monotone unless operation is explicit repair;
- every search hit ref is readable;
- no DB row points at a missing committed blob;
- FTS reconciliation query is clean;
- conflicts preserve both hashes and never overwrite original entries.

Concurrency variant: run duplicate and disjoint ingests in parallel under
`go test -race`; final state must equal some sequential order.

### Depot state model

Model state:

```text
objects: map[bundleSHA]BundleBytes
catalogShards: map[machineID]set[bundleSHA]
```

Commands:

- put bundle;
- put duplicate;
- list;
- fetch;
- introduce stale catalog ref;
- delete catalog shard;
- verify;
- repair;
- concurrent same-machine catalog update.

Properties:

- object keys match bytes;
- list is deterministic sorted union;
- repair recreates catalog from objects;
- stale refs are removed or reported;
- local and fake-R2 drivers satisfy the same contract.

### Fuzz targets

Keep and strengthen existing fuzz targets:

- parser JSONL: no panic; raw file preserved; valid parsed entries have non-empty
  IDs; malformed lines produce diagnostics; no invalid UTF-8 crash.
- canonical ref codec: no panic; valid `msg:v1`, `session:v1`, and
  `artifact:v1` refs round-trip; formatting is stable; legacy refs are rejected.
- archive walk/write: manifest/data/checksum honesty; path safety; budget
  accounting.
- depot address/key: valid-or-typed-error; normalized form stable.

Add structure-aware fuzzers when the constructors exist:

- fuzz bundle → ingest → corpus invariant check;
- fuzz manifest mutations → validation rejects or yields safe typed error;
- differential parser fuzzing only when replacing an active parser with another active parser; no legacy compatibility harness is required pre-release.

### Exhaustive checks

Small finite spaces should be exhaustive, not sampled:

- every `Role × index_tool_output` combination;
- every `RefKind`/`AssetKind`/`CopyState` codec value;
- all ingest permutations for N ≤ 5 small disjoint bundles;
- output mode mutual exclusions;
- depot address forms (`local`, `local:`, `r2`, `r2:bucket`, bad schemes).

### Mutation testing

Run `gremlins` outside normal CI on invariant-critical packages:

```bash
scripts/verify.sh mutation-dry
scripts/verify.sh mutation
```

Start with dry-run to inventory covered mutants. Then run mutation tests on:

- `./internal/model`
- `./internal/corpus`
- `./internal/archive`
- `./internal/depot`
- `./internal/adapters`

A surviving mutant in identity/ref parsing, archive validation, conflict
quarantine, depot key validation, or path safety is a release blocker.

### Static debt inventory

Before refactoring, freeze known correctness debt so it cannot spread:

- ambient `time.Now`/`time.Sleep` sites;
- raw identity concatenation sites;
- direct FTS write sites;
- network imports outside depot;
- focused/sleep-based/log-only tests.

As each phase replaces a pattern with construction, shrink the allowlist. The
static test changing from “known debt inventory” to “hard ban” is part of the
phase definition of done.

## Migration plan

### Phase 0 — Prep and guardrails

No behavior change. Phase 0 exists to make later behavior-changing refactors smaller, more mechanical, and safer.

Required guardrails before production refactors:

1. **Keep expanding static debt inventories before refactors.** Freeze known raw identity construction, ambient time/sleep, manual FTS writes, broad path authority, direct SQL mutation of append-only tables, network imports outside depot, and weak-test patterns. Start with allowlisted known debt; shrink the allowlists as each phase removes debt.
2. **Add state-machine skeletons for corpus and depot before changing storage.** The first skeleton may characterize current behavior and skip unimplemented commands, but it must define the model state, commands, invariant checks, and replay format before schema/identity changes begin.
3. **Add adapter conformance fixtures before parser rewrites.** Current Pi, Claude Code, Codex, and OpenCode normalization behavior must be captured as data-driven raw-input → normalized-entry/diagnostic fixtures before replacing parser internals.
4. **Add schema introspection helpers before FK/trigger migrations.** Tests should be able to assert “table has FK/check/trigger/index X” and run direct-SQL Type B attempts without hand-parsing all schema text in each test.
5. **Introduce seams by name first.** Add behavior-preserving wrappers/interfaces named `ReadCanonical`, `ResolveHuman`, `Clock`, `Sleeper`, and source read capability before changing their internals. Names first, behavior second.
6. **Add corpus/depot verifier queries before enforcing stricter constraints.** A verifier should detect current drift/missing blobs/stale catalog refs before migrations make those states impossible or repairable.
7. **Run mutation dry-runs periodically before relying on tests.** `scripts/verify.sh mutation-dry` should be run after adding major invariants and before deleting duplicate runtime checks; use its uncovered/surviving areas to strengthen tests.

Supporting guardrails:

- maintain `scripts/verify.sh` and Make targets as the single local/CI verification entrypoint;
- keep `rapid` and shared generators available for shrinkable PBT;
- add differential tests whenever old and new implementations coexist.

Exit criteria:

- `scripts/verify.sh full` passes;
- `scripts/verify.sh mutation-dry` completes and its gaps are triaged;
- current known debt is inventoried by static tests;
- corpus/depot state-machine skeletons compile and can replay a minimal sequence;
- adapter conformance fixture harness exists for all built-in adapters;
- schema introspection helpers exist for future Type B tests;
- `ReadCanonical`, `ResolveHuman`, `Clock`, `Sleeper`, and source read-capability seams exist;
- corpus/depot verifier queries can detect at least one seeded inconsistency each;
- new PBT/fuzz/state-machine tests can be added without bespoke setup.

### Phase 1 — Typed primitives, current storage/wire format

- Add `EntryID`, `SessionKey`, `Role`, `AssetKind`, `CopyState`, SHA types.
- Keep DB/string wire representation current, but reject pre-release legacy encodings rather than preserving them.
- Parse at boundaries; internal code accepts typed values.
- Add exhaustive role/index tests and constructor Type A/Type B tests.

Exit criteria:

- documented behavior unchanged except intentional pre-release cleanup;
- no new string concatenation for keys;
- constructors carry the invariant; downstream duplicate validation removed only
  when it defends the same non-adversarial failure mode.

### Phase 2 — Ref codec and canonical read split

- Introduce ref sum type and versioned codec.
- `search` emits canonical refs.
- `read` path splits into `ReadCanonical` and `ResolveHuman`.
- Legacy ref syntax is removed pre-release; malformed or old refs fail fast.

Exit criteria:

- every search hit canonical ref reads exactly;
- ambiguity exists only in `ResolveHuman`;
- PBT/fuzz prove codec stability.

### Phase 3 — Storage invariants

- Add FK/CHECK constraints and append-only/quarantine triggers via migrations.
- Keep FTS trigger-maintained and maintain a reconciler so raw tables remain
  the source of truth.
- Add Type B direct-SQL tests for orphan rows, mutation attempts, and conflicts.

Exit criteria:

- schema rejects invalid states;
- state-machine tests pass under `-race`;
- static FTS direct-write allowlist shrinks.

### Phase 4 — Canonical encoder, clock, capabilities

- Canonical archive/manifest encoder API.
- `Clock`/`Sleeper`/`Backoff` seams.
- read-only source capabilities and root capabilities.
- Static time debt inventory becomes a hard ban outside the production clock
  construction site.

Exit criteria:

- deterministic output properties use fixed clocks;
- retry tests use no real sleep;
- source write authority is not available to adapters.

### Phase 5 — Identity v2 and schema cutoff

- Bump bundle/corpus identity schema when necessary.
- Reject unsupported bundle schemas at validation/read boundaries.
- Store only v2 `sk1_...` session keys; do not create alias tables for old delimiter keys.

Exit criteria:

- unsupported bundles fail with clear errors;
- new bundles use v2 identity only;
- fixture bundles and state-machine sequences cover the cutoff behavior.

## What to do in advance to make implementation simpler and safer

These are Phase 0 tasks and can be done before production refactors. They are intentionally front-loaded because they reduce the risk and blast radius of the later implementation.

1. **Keep expanding static debt inventories before refactors.** Static tests should fail if new raw identity construction, ambient time/sleep, direct FTS writes, raw SQL mutations of append-only tables, network imports outside depot, path writes outside corpus/depot roots, or weak-test patterns appear. Begin with explicit allowlists of current debt; every refactor that removes debt must shrink the allowlist in the same commit. If a category reaches zero, convert the inventory into a hard ban.
2. **Add state-machine skeletons for corpus and depot before changing storage.** Create minimal `rapid` state-machine tests with model state, commands, invariant checks, and replay traces before schema changes. The first version may only execute `ingest duplicate`, `read`, `search`, `status`, `depot put/list/fetch/verify`, and seeded failure cases, but the structure must be present before adding FK/trigger/identity migrations.
3. **Add adapter conformance fixtures before parser rewrites.** Capture current Pi, Claude Code, Codex, and OpenCode behavior as JSON fixtures: raw input/file metadata → normalized `ParsedEntry`/asset/diagnostic expectations. Run all adapters through one shared harness. This makes parser refactors differential and prevents “typed parser” work from silently changing importer semantics.
4. **Add schema introspection helpers before FK/trigger migrations.** Provide test helpers for `HasTable`, `HasColumn`, `HasIndex`, `HasForeignKey`, `HasCheck`, `HasTrigger`, and direct-SQL Type B attempts. Migration tests should assert both the structural object exists and the invalid operation is rejected.
5. **Introduce seams by name first: `ReadCanonical`, `ResolveHuman`, `Clock`, `Sleeper`, source read capability.** Add narrow wrappers before semantic changes. This lets call sites move mechanically, lets tests target the future API early, and avoids mixing naming, behavior, and storage changes in one diff.
6. **Add corpus/depot verifier queries before enforcing stricter constraints.** Implement lightweight queries that find orphan messages/assets/artifacts, FTS drift, missing blobs, wrong blob hashes where cheap, stale depot catalog refs, malformed depot keys, and missing catalog shards. The verifier should run against current stores and produce actionable diagnostics before migrations start rejecting these states.
7. **Run mutation dry-runs periodically to find weak tests before relying on them.** Run `scripts/verify.sh mutation-dry` after each major guardrail and before deleting duplicate runtime checks. Track uncovered critical mutants as work items; do not use a new constructor/schema trigger as justification for deleting old checks until mutation dry-run and targeted Type A/Type B tests show the invariant is covered.

Additional advance work:

8. **Use one verification entrypoint.** `scripts/verify.sh` and Make targets define quick/full/fuzz/mutation profiles so every phase runs the same checks locally and in CI.
9. **Add shrinkable generators now.** Even small `rapid` generators for refs, catalog refs, manifests, sessions, and operation traces reduce bespoke property-test setup later.
10. **Use side-by-side paths only for active formats.** Differential tests compare old and new implementations when both are intentionally supported; pre-release legacy refs/bundles should be deleted and covered by rejection tests instead of compatibility shims.

## Additional verification upgrades

These are not all prerequisites for Phase 1, but they improve the quality of the suite and should be pulled forward whenever they are cheap.

### CLI journey tests with `testscript`

Add `testscript`-style CLI contracts for full user journeys:

- `init --accept-secrets → refresh → search --refs → read <ref>`;
- explicit bundle-path ingest into alternate `--repo`;
- depot publish/list/fetch/verify/repair;
- JSON error envelope behavior for bad flags, bad refs, missing config, and bad depot credentials;
- no network for local-only commands.

These tests should assert stdout/stderr, exit code, filesystem effects, and JSON shape. They complement Go unit tests because `aha` is primarily a CLI contract.

### Golden update discipline

Golden tests should have an explicit update flag or helper, stable normalization, and reviewable diffs. Add goldens for:

- representative JSON command outputs;
- Markdown renderers;
- one tiny canonical bundle/manifest once the canonical encoder lands;
- doctor/verify diagnostics with stable fake paths.

Do not snapshot large opaque blobs unless the bytes are the contract.

### Fuzz corpus management

When fuzzing finds a bug, commit the minimized input under `testdata/fuzz` and add a named regression when the failure explains an invariant. Fuzz targets should graduate from “no panic” to postconditions:

- parser: raw preserved, non-empty IDs for valid entries, diagnostics for malformed lines;
- refs: canonical-only round trips, stable formatting, and rejection of legacy syntax;
- archives: manifest/data/checksum/path/budget honesty;
- depot: address/key valid-or-typed-error and stable normalization.

### Custom static analysis where grep becomes weak

The debt inventory can start as tests, but high-value invariants should become AST or `go/analysis` checks when substring matching becomes too easy to evade:

- no `time.Now`/`time.Sleep` outside clock/backoff packages;
- no direct writes to `fts_*` outside schema/reconciler code;
- no raw SQL `DELETE`/`UPDATE` on append-only tables outside migrations/repair;
- no network imports outside `internal/depot`;
- no source-path write authority in adapters;
- no new string-built identity keys.

### Failure-injection and crash-window tests

Keep extending hooks/fakes that can stop execution between durable steps:

- after staging bundle, before DB transaction;
- after rows inserted, before blob promotion;
- after blob promotion, before commit;
- during catalog shard conditional write conflicts;
- during verify/repair partial failure.

The oracle is not “no leftovers ever.” The oracle is “no committed row points at missing/wrong bytes; leftover staging/orphan blobs are detectable and repairable.”

### Version-cutoff matrix tests

Every schema or bundle/corpus version bump needs a matrix:

| Input | Operation | Expected |
|---|---|---|
| empty corpus | open/migrate | current schema |
| current populated corpus | reopen/status/search/read | counts and canonical refs preserved |
| unsupported bundle schema | ingest/read manifest | clear typed error; no corpus mutation |
| interrupted migration | reopen | either rolls forward or reports repairable error |

Use real SQLite files and committed tiny fixtures.

### Coverage and mutation triage, not vanity thresholds

Line coverage should be used to find untested branches, not as a release badge. Mutation output is more useful for invariant-critical code. Track:

- uncovered mutants in critical files;
- surviving mutants after a real mutation run;
- equivalent-mutant rationale;
- which Type A/Type B/property/fuzz test was added to kill a non-equivalent mutant.

### Replayable state-machine failures

State-machine tests should print enough to replay failures deterministically:

- random seed;
- minimized operation list;
- generated bundle/ref/catalog summaries;
- temp corpus/depot preservation hint when `AHA_KEEP_FAILED_TESTDATA=1` is set.

A minimized operation trace should be easy to convert into a named regression test.

### Fixture realism and fixture minimization

Keep two fixture classes separate:

- **realish fixtures** from observed Pi/Claude/Codex formats, sanitized only where necessary;
- **minimal fixtures** for focused invariants.

Realish fixtures catch drift; minimal fixtures make failures easy to understand. Do not let generated fixtures replace realish adapter examples.

### Verify command as test oracle

As corpus/depot verifier queries mature, tests should call the same verifier code that `doctor`, `aha verify`, and `aha depot verify` use. This avoids a parallel “test-only validator” drifting away from the user-facing repair tool.

## Definition of done

- Every invariant in the inventory has a construction mechanism or a documented
  reason construction cannot fully apply.
- Every mechanism has a Type A proof test; every rejecting mechanism has a Type B
  invalid-state test.
- `ReadCanonical` has no ambiguous branch; `ResolveHuman` owns ambiguity.
- No direct FTS writes outside schema/reconciler code.
- No ambient wall-clock/sleep calls outside clock/backoff construction.
- Parser fuzzing asserts postconditions, not only “does not panic.”
- Corpus and depot state machines cover duplicate, conflicting, interrupted,
  repair, and concurrent sequences.
- `gremlins` has no surviving mutants in identity/ref/archive/path/depot-key/
  conflict-quarantine critical paths, or surviving mutants are documented with
  justified equivalent-mutant rationale.
- Bundle/corpus v2 ships with tested rejection of unsupported schemas and no legacy identity aliases.

## Highest-leverage order

1. Freeze debt and unify verification commands.
2. Ref sum type + canonical/human read split.
3. Typed `EntryID`/`SessionKey` constructors with v2-only storage.
4. Schema constraints/triggers for append-only, conflict quarantine, and FKs.
5. FTS trigger/reconciler work for messages and artifacts.
6. Clock/backoff/source capability seams.
7. Identity v2 cutoff with explicit unsupported-schema/ref rejection tests.
