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
  - go
  - specification
source_type: design
status: draft-spec
aliases:
  - CbC refactor
  - aha correctness spec
---

# Correctness by Construction Refactor Spec

## Purpose

`aha` is correct today by **test-and-convention**: its invariants (determinism,
idempotency, append-only merge, conflict quarantine, read-only sources,
search/read coherence) live in imperative code paths guarded by tests. The
system even has the right instinct — "never delete an entry; add, link, or
quarantine" — but enforces it by remembering not to write `DELETE`, not by
making deletion impossible.

This spec defines a refactor that moves each invariant from *"a test will catch
us if we break it"* to *"the broken code does not compile, the database rejects
it, or the value cannot be constructed."* Where construction cannot reach (the
untyped JSON boundary, cross-run determinism, merge algebra), the residual risk
is pinned by **property-based testing (PBT)**, **state-machine / model-based
testing**, and **fuzzing** — not by example tests alone.

This is an engineering-quality refactor, not a feature change. User-visible
behavior is preserved except where the identity format necessarily bumps to a
new bundle/corpus version (Phase 4).

## Non-goals

- No new product features (no semantic search, redaction, new adapters).
- No rewrite. Package boundaries (`cmd/aha`, `internal/*`) stay.
- No move off SQLite, `modernc.org/sqlite`, `tar.zst`, or stdlib `flag`.
- No attempt to type the *raw* input. External agent JSONL stays untyped and
  lossless; only the normalized projection gets strong types.
- No formal proofs or a dependently-typed rewrite. "Construction" here means
  smart constructors, closed types, encapsulation, and database constraints —
  the strongest guarantees Go and SQLite actually provide.

## Principles

1. **Make illegal states unrepresentable.** A value that violates an invariant
   should not be constructible. Prefer a type over a check, a constraint over a
   convention.
2. **Parse, don't validate.** Untyped input crosses into typed values exactly
   once, at one fallible constructor per source, which yields either a
   well-formed value or a recorded diagnostic.
3. **Open-world boundary.** The raw blob path stays permissive and lossless.
   Unknown roles, fields, or block types degrade to "stored raw + diagnostic,"
   never to a panic or a dropped row. Strong types live on the corpus side of
   the boundary; permissive bytes on the source side. This protects the top
   invariant (never lose an entry) from over-typing.
4. **Total functions over partial ones.** Reads and codecs should be total:
   `ref -> context | typed-not-found`, never "ambiguous at runtime."
5. **Constraints are the source of truth.** Storage invariants belong in the
   schema (foreign keys, `CHECK`, `UNIQUE`, triggers), not in the ordering of
   imperative inserts.
6. **Determinism by encoder.** Canonical output comes from a type that can only
   emit one byte sequence, with golden tests as a backstop rather than the
   primary guarantee.
7. **Verification matches the risk class.** Types eliminate a class of bugs;
   PBT/state-machine/fuzz guard the residue that types cannot reach.

## Invariant inventory

Each invariant gets a construction-level mechanism *and* a generative test that
fails if the mechanism is removed. Convention-only enforcement is not done.

| Invariant | Enforced today | Target construction | Primary verification |
|---|---|---|---|
| Stable identity, no collisions | string concat `source+":"+machine+":"+sid` (`ingest.go:363`) | `SessionKey`/`EntryID` value types; content-addressed canonical encoding | PBT injectivity + fuzz codec |
| Ref round-trips | `HitRef` struct of optionals + `firstNonEmpty` smear (`model.go:18-75`) | `HitRef` sum type with one total codec | PBT round-trip + fuzz |
| Read is total | runtime "ambiguous session/entry" (`read.go:77-144`) | unique keys ⇒ lookup; fuzzy human match split out | state-machine: every entry readable |
| Index decision is total | role string switch with silent default (`ingest.go:438-450`) | closed `Role` type; exhaustive decision | PBT over all roles + lint |
| Idempotent ingest | `insert or ignore` + attempt logic (`ingest.go:39-69`) | `UNIQUE` constraints encode dedup | state-machine: `ingest∘ingest = ingest` |
| Append-only merge | absence of `DELETE`/`UPDATE` | trigger rejects delete/update on `entries` | state-machine: entries monotone |
| Conflict quarantine | imperative select-then-branch (`ingest.go:386-402`, `452-469`) | `INSTEAD OF` trigger routes same-id/diff-hash to `conflicts` | state-machine: exactly-one + conflict row |
| FTS never drifts | hand dual-write (`ingest.go:412-421`) | FTS5 external-content + triggers | state-machine: `messages ↔ fts` agree |
| Deterministic bundles | golden byte compare | canonical encoder type | PBT: encode is a function; fuzz inputs |
| Read-only sources | static grep tests + path checks (`safety/paths.go`) | source handle exposes no write methods | type (compile) + retained static guard |
| Parser never crashes | `FuzzParseGenericJSONL` (`parser_test.go:11`) | constructor always yields non-empty id + raw | fuzz with post-conditions |

Note the one place the codebase already does construction right: content-addressed
blobs are temp-written and atomically renamed, never overwritten in place
(`ingest.go:713-763`). The refactor applies that same "never overwrite" discipline
to rows and identity.

## Foundational primitive changes

### Identity as a constructed type

`session_key` and `entry_id` stop being free strings.

- Introduce `SessionKey` and `EntryID` as opaque value types in `internal/model`
  with unexported fields and the **only** constructor taking the component tuple.
- Canonical encoding is the SHA-256 of a length-prefixed encoding of
  `(source, machine_id, source_session_id)` — fixed width, delimiter-free,
  collision-resistant, content-addressed like everything else. This removes the
  `":"`/`"#"` fragility in `ingest.go:363` and the artifact-key string prefix
  scheme (`model.go:25-33`).
- `EntryID` derivation (the `line-%06d-hash` fallback at `parser.go:57`) becomes
  a constructor that *always* returns a non-empty `EntryID`, so "entry id
  present" is a type guarantee, not a runtime hope.

Consequence: unique-by-construction keys delete the ambiguity branches in
`resolveSession`/`resolveEntryLine` (`read.go:77-144`). `read` becomes a total
lookup; fuzzy human prefix matching moves to a separate, clearly fallible
`Resolve(humanQuery) -> SessionKey | NotFound` used only by the CLI.

### `HitRef` as a sum type

`HitRef` carries `SessionKey`, `EntryID`, and `ArtifactSHA` with `firstNonEmpty`
fallbacks (`model.go:18-75`) — three fields encoding one of two shapes. Replace
with a closed union: `MessageRef{SessionKey, EntryID}` | `ArtifactRef{SHA}`,
modeled as a sealed interface (unexported method) so no third package can add a
variant and every switch is exhaustive. `FormatHitRef`/`ParseHitRef` collapse to
one total codec. Better: `search` emits the ref `read` consumes directly, so
search/read coherence is a type-level fact, not a string contract re-parsed at
the boundary.

### Closed enums replace stringly types

`Role`, `EntryKind`, `HitKind`, `AssetKind`, `CopyState`, and source `type`
become defined types produced only by adapter normalization. The payoff is
concrete: `shouldIndexText` (`ingest.go:438-450`) currently falls through to
"don't index" on any unrecognized role — exactly the class of the real-Pi
`message.role` bug recorded in lessons-learned. A closed `Role` makes the
indexing decision total over a known set; an unseen role is a normalize-time
decision, not a silently dropped index.

### One typed parse boundary

Replace scattered `map[string]any` access (`stringField`/`nestedString`/
`numField` in `parser.go`) with one fallible constructor per source that returns
a fully typed `ParsedEntry` *or* a diagnostic. The `any` and `""`-default values
stop leaking down the pipeline. The raw line is always preserved regardless of
normalization outcome (open-world principle).

## Storage as the invariant layer

The schema is almost entirely untyped `text` columns with no foreign keys
between `sessions`/`entries`/`messages` (`schema.go:17-21`). Migrate to:

- Foreign keys `messages → entries → sessions` (and `fts` bound to `messages`).
- `CHECK` constraints for `role`, `entry_type`, `copy_state`, `kind`.
- FTS5 **external-content** tables with insert/update/delete triggers so
  `messages` and `fts_messages` cannot drift (replaces the manual dual write).
- **Quarantine-as-constraint:** a unique index on `(session_key, entry_id)` plus
  an `INSTEAD OF INSERT` trigger that, on same id with a different `entry_sha256`,
  writes a `conflicts` row and refuses to overwrite. The database enforces "never
  overwrite," replacing the select-then-branch logic in `ingest.go:386-402`.
- **Append-only:** triggers reject `DELETE`/`UPDATE` on `entries`.
- **Idempotency as uniqueness:** dedup identity is a `UNIQUE` constraint, so
  re-ingest is a no-op by the schema, not by `insert or ignore` racing alongside
  `recordBundleAttempt` (`ingest.go:39-69`).

All schema changes ship as idempotent migrations with old-shape tests, per the
existing migration discipline (`schema.go:40-77`).

## Determinism and capabilities by construction

- A canonical-encoder type for the manifest and tar stream whose API can only
  build sorted file lists with zeroed mtimes/uids and a pinned zstd level —
  nondeterminism becomes unrepresentable. Golden tests stay as a backstop.
- The snapshot path accepts a read-only source capability (an `fs.FS`-style
  handle with no write methods) instead of raw paths, so writing to a source is
  a compile error. The static no-mutation/no-network guards
  (`adapters/read_only_test.go`, `internal/testquality`) are retained as defense
  in depth, not as the only line.

## Verification strategy

Types remove whole classes of bugs. The remaining risk lives where types stop:
the untyped input boundary, cross-run determinism, and the algebra of merging
many bundles in any order. Three generative techniques cover that residue.

### Property-based testing (PBT)

Add `pgregory.net/rapid` (shrinking, deterministic seeds, stateful support).
`testing/quick` is insufficient — no shrinking, weak generators. PBT asserts
algebraic laws over generated inputs, not hand-picked examples:

- **Codec round-trip:** `parse(format(ref)) == ref` for all `ref`;
  `format(parse(s))` is stable for valid `s` and a typed error otherwise.
- **Identity injectivity & determinism:** distinct component tuples produce
  distinct `SessionKey`s; equal tuples produce equal keys.
- **Index totality:** every value of the closed `Role` type maps to a defined
  index decision (no default branch).
- **Determinism:** `encode(inputs)` is a pure function — same inputs, identical
  bytes, across repeated runs and shuffled discovery order.
- **Search/read coherence, generalized:** for a generated corpus and query,
  every hit in `search` is readable and the returned window contains the hit.
- **Merge algebra:** ingest of disjoint bundles commutes; ingest is monotone
  (append-only); re-ingest is idempotent.

Generators live in `internal/testutil` (e.g. `GenParsedSession`,
`GenBundle`, `GenRef`) so properties share realistic, shrinkable inputs.

### State-machine / model-based testing

The corpus is a state machine; bugs hide in *sequences* of operations
(re-ingest after conflict, busy-retry during duplicate detection — today only
narrowly covered by `concurrency_test.go`). Using `rapid`'s stateful testing:

- **Model:** an in-memory abstraction — `map[SessionKey][]EntryID`, a conflict
  set, a seen-bundle set, a blob set.
- **Commands:** `Snapshot`, `Ingest`, `ReIngest(sameBundle)`,
  `IngestConflicting(sameIdDiffHash)`, `Read(ref)`, `Search(q)`, `Status`.
- **After every command**, assert the real SQLite store matches the model and
  the invariants hold: counts agree, entries are monotone, no orphan
  `messages`/`fts` rows, every entry is readable, same-id/diff-hash yields
  exactly one live entry plus one conflict row, status counts are exact.
- `rapid` generates random command sequences and shrinks any failure to a
  minimal reproducer.
- **Concurrency variant:** parallel ingest of same and different bundles under
  `go test -race`; assert the final state equals some sequential order
  (linearizable), strengthening the existing busy-retry coverage.

This is the layer that proves the schema-level invariants (append-only,
quarantine, idempotency) actually hold under interleaving, not just in isolation.

### Fuzzing

Strengthen the three existing fuzz targets from "does not panic" to "preserves
post-conditions," and add structure-aware targets:

- `FuzzParseGenericJSONL` (`parser_test.go:11`): every line yields a non-empty
  `EntryID`, the raw line is preserved, and malformed input produces a recorded
  diagnostic — never a panic, never a dropped raw line.
- `FuzzHitRefParseFormat` (`hitref_fuzz_test.go:27`): retarget to the sum-type
  codec; assert total round-trip and that no input panics.
- `FuzzWalkBundleRoundTrip` (`archive_fuzz_test.go`): keep; extend to assert
  manifest honesty (every archived file in the manifest and vice versa).
- **New — structure-aware bundle fuzz:** a fuzz seed builds a whole bundle, then
  ingest must leave every corpus invariant intact (fuzz seed feeds the
  state-machine model check). Catches ingest corruption that line-level fuzzing
  misses.
- **New — differential fuzz:** the canonical encoder and any second
  serialization path must agree byte-for-byte.
- CI runs fuzz targets with a bounded time budget; the discovered corpus is
  committed under `testdata/fuzz` so regressions stay covered.

### Static guards

Extend `internal/testquality` to prevent regression toward convention:
ban raw string concatenation to build identity keys, ban `map` iteration inside
canonical encoders, and require enum switches to be exhaustive (an
`// exhaustive` marker test or a vet-style check). These keep the construction
guarantees from eroding in later changes.

## Migration plan

Characterize first, then tighten — types and constraints are introduced behind a
safety net, smallest blast radius first. The identity-format change is last
because it bumps the bundle/corpus version.

- **Phase 0 — Safety net.** Add `rapid`, the model, generators, and the
  state-machine harness against *current* code. Capture present behavior as
  characterization. No behavior change. (Per the testing-best-practices
  "characterize before refactor" lesson.)
- **Phase 1 — Typed primitives, same wire format.** `SessionKey`, `EntryID`,
  `Role`, `HitKind`, etc. as newtypes with smart constructors; centralize the
  codec. Wire/DB representation unchanged. PBT the codecs and enums.
- **Phase 2 — `HitRef` sum type + opaque handle.** `read` becomes total over
  canonical refs; fuzzy human resolution split into its own function. PBT +
  fuzz round-trip; state-machine asserts every entry readable.
- **Phase 3 — Storage invariants.** Foreign keys, `CHECK`, FTS triggers,
  quarantine/append-only triggers, idempotency uniqueness, with migrations and
  old-shape tests. State-machine tests now assert the DB *rejects* violations.
- **Phase 4 — Determinism encoder, capabilities, content-addressed identity.**
  Canonical-encoder type and read-only source capability land first
  (non-breaking). Content-addressed identity is the breaking step: bump
  `BundleSchema` to `v2` (`model.go:9`) and ship a `v1 → v2` ingest path that
  re-keys on ingest so existing bundles still merge.

Each phase keeps `go test ./...`, `go test -race ./...`, and the fuzz/PBT suites
green before the next begins.

## Definition of done

- Every row in the invariant inventory has a construction-level mechanism *or* a
  documented reason it cannot have one (open-world boundary cases).
- No invariant relies on convention alone; each has a PBT, state-machine, or
  fuzz test that fails if the mechanism is removed.
- `read` has no runtime "ambiguous" path for canonical refs; ambiguity exists
  only in the explicitly fallible human resolver.
- FTS, conflict quarantine, append-only, and idempotency are enforced by schema
  objects, verified by the state machine under interleaving and `-race`.
- The parser fuzz target asserts post-conditions (non-empty id, raw preserved,
  diagnostics on malformed), not just absence of panic.
- `testquality` guards block raw key concatenation, map iteration in encoders,
  and non-exhaustive enum switches.
- `BundleSchema v2` ships with a tested `v1 → v2` ingest path; no existing
  bundle becomes unreadable.

## Tradeoffs and accepted limits

- **Go's type system is the ceiling.** No native sum types, no enforced
  exhaustiveness, no non-empty-string refinement. "Construction" here is mostly
  encapsulation + smart constructors + DB constraints + retained static guards.
  Sealed interfaces and `testquality` checks approximate exhaustiveness; they do
  not replace a compiler that enforces it.
- **The input is irreducibly untyped and evolving.** Over-tight types would turn
  "a role/field we have never seen" into an ingest failure, violating the top
  invariant. The open-world rule is non-negotiable: raw stays lossless, only the
  normalized projection is typed, unknown input degrades to raw + diagnostic.
- **Breaking format change.** Content-addressed identity requires `v2` bundles
  and a migration. For a single-user local tool this is the highest-cost change;
  it is sequenced last and gated behind the `v1 → v2` ingest path.
- **Generative-test cost.** PBT and state-machine tests are slower and can be
  flaky if seeds are not pinned. Seeds are fixed in CI; failures must shrink to a
  committed regression case before the fix is considered done.
- **Trigger complexity.** Moving quarantine/append-only into triggers trades
  imperative clarity for schema-enforced guarantees; the state machine is what
  keeps that trade honest.

## Highest-leverage first step

If only one change ships: make identity content-addressed and turn `HitRef` into
a sum type with one total codec (Phases 1–2). That single primitive change
collapses a family of runtime failures — ambiguous reads, ref round-trip bugs,
delimiter collisions, the artifact-field smear — and it is the primitive the rest
of the architecture hangs on. Quarantine-as-constraint (Phase 3) follows, because
it finishes the "never overwrite" instinct the blob writer already demonstrates.
