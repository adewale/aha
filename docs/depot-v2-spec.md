---
title: Depot v2 — Content-Addressed Snapshots Spec
kind: spec
created: 2026-06-09
updated: 2026-06-10
tags:
  - aha
  - depot
  - content-addressing
  - snapshots
  - scalability
  - shlemiel
  - cloudflare-r2
  - correctness-by-construction
  - tdd
  - go
  - specification
source_type: design
status: implemented
aliases:
  - depot v2
  - CAS depot
  - manifest snapshots
---

# Depot v2 — Content-Addressed Snapshots Spec

## Status and relationship to v1

This design is **implemented** (phases 0-7 below are complete; the phase plan
is retained as the record of how, and of which tests pin what). It supersedes the *bundle-store layout* of
`docs/r2-snapshot-aggregation-spec.md` while keeping everything else that spec
locked in: the guiding principle (**data loss outweighs data theft**), the
trust boundaries in `docs/trust.md` (network only inside `internal/depot` +
`internal/server`/`internal/mcp`), the local-by-default posture, and the rule
that the corpus is always rebuildable from the depot.

`aha` has **no users yet**, so this spec deliberately takes the same one-time
luxury the v1 depot spec took: **no migration shims, no dual-format drivers,
no compatibility window.** The v2 layout replaces the v1 layout outright. The
single compatibility hedge we keep is nearly free and independently useful:
`aha ingest <bundle.tar.zst>` continues to read v1 bundle *files* (journey 4
in `docs/user-journeys.md` wants this anyway), so any v1 depot ever needed
again is recoverable by feeding its bundles through import.

Vocabulary (one name per entity, as in v1): the unit of stored data is a
**blob** (one compressed session/artefact file version, addressed by SHA-256);
the snapshot artefact is a **manifest** (a small JSON object listing blob
addresses, addressed by its own SHA-256); the searchable database is the
**corpus**. The word **bundle** is reserved for the v1 `tar.zst` file format,
which survives only in `export`/`import`.

## Problem: Shlemiel the Painter at three layers

The v1 design is correct but repaints the whole road every morning
(<https://wiki.c2.com/?ShlemielThePainter>). With history size H, machines M,
and daily refreshes, all of the following grow with **total history**, not
with the day's delta:

1. **Capture is full-state.** `archive.Capture` re-reads, re-hashes, and
   re-parses every session file on every snapshot, including years-old
   unchanged sessions. `SkipIfUnchanged` runs *after* capture, so it saves the
   push but never the scan.
2. **Any change produces a full-history bundle.** One new session means the
   new bundle contains everything the previous one contained plus the delta.
   Depot storage is the integral of history size — **O(N·H), quadratic over
   time** — and every refresh re-uploads the entire history.
3. **Ingest re-parsed the entire bundle.** Fixed pre-spec: ingest now skips
   session-file versions already proven present (`knownSessionVersionKey` in
   `internal/corpus/ingest.go`, pinned by
   `TestUnchangedSessionFileSkipsReparseAcrossBundles`). Before the fix,
   `refresh` parsed its own just-pushed bundle a second time and every machine
   re-parsed the fleet's full history daily.
4. **Every corpus hoarded every bundle blob.** `PromoteBundle` copies each
   ingested bundle into the corpus and `referencedBlobPaths` marks every
   bundle referenced, so `corpus prune-orphans` reclaims nothing in normal
   use: per-corpus disk is quadratic too.
5. **The cliff.** Because capture is full-state, the fixed bundle budgets
   (`MaxBundleBytes` = 2 GiB, `MaxBundleUncompressedBytes` = 8 GiB) are a
   hard failure every heavy user eventually hits, with no graceful path.

v2 makes these states **unrepresentable** instead of optimised: there is no
full-history artefact to re-upload, no monolith to hit a budget, no bundle
blob to hoard, and "already stored" is an address collision rather than a
runtime check.

## Prior art and literature (why this exact shape)

Forty years of systems converge on two independent layers of incrementality —
a **scan layer** (skip *reading* unchanged inputs) and a **storage layer**
(skip *storing* known data). Either alone removes most of the Shlemiel cost;
together cost ≈ O(delta).

- **restic / borgbackup / kopia**: chunks/files stored once in a
  content-addressed store; a snapshot is a small manifest of hashes; a parent
  snapshot's stat data (size/mtime/inode) short-circuits re-reading unchanged
  files. "Each snapshot is always incremental" while remaining *logically
  full*.
- **git**: the index is the diff baseline (`git status` stats, it does not
  hash); packfiles are content-addressed; fetch negotiates haves/wants and
  transfers only missing objects. Git's own Shlemiel history (untracked
  cache, fsmonitor, commit-graph) is the cautionary tale for scan cost.
- **Time Machine / ZFS / btrfs**: every snapshot *looks* full but unchanged
  data is shared by reference (hard links / CoW) — "logically full,
  physically incremental" is the strongest pattern in the family and is what
  v2 adopts. Chain-based alternatives (Postgres base-backup+WAL, Veeam
  forward incrementals) get the same cost profile but add **chain fragility**:
  a lost delta silently truncates reconstructable state. v2 rejects chains.
- **Dynamo anti-entropy / Merkle (CRYPTO '87)**: compare states by exchanging
  small digests, fetch only differences. At aha's scale a flat manifest is
  the digest; a Merkle tree is a deferred optimisation.
- **Log shippers (Filebeat/fluentd registries)**: for append-mostly logs,
  `(prefix-hash, parsed-offset)` bookkeeping gives O(appended bytes) parsing
  with no rolling-hash machinery. Reserved here as a *future* refinement for
  growing session files; explicitly out of scope for v2.

Key citations (verified against primary venues): Muthitacharoen, Chen &
Mazières, *A Low-Bandwidth Network File System* (SOSP 2001); Zhu, Li &
Patterson, *Avoiding the Disk Bottleneck in the Data Domain Deduplication
File System* (FAST 2008); Xia et al., *FastCDC* (USENIX ATC 2016); Tridgell &
Mackerras, *The rsync algorithm* (ANU TR-CS-96-05, 1996); Acar,
*Self-Adjusting Computation* (CMU, 2005); McSherry et al., *Differential
Dataflow* (CIDR 2013); Tarjan, *Amortized Computational Complexity* (SIAM
1985); Merkle, *A Digital Signature Based on a Conventional Encryption
Function* (CRYPTO '87); DeCandia et al., *Dynamo* (SOSP 2007); O'Neil et al.,
*The LSM-Tree* (Acta Informatica 1996); Rosenblum & Ousterhout, *LFS* (TOCS
1992); Korn et al., RFC 3284 (VCDIFF, 2002); Paulo & Pereira, *A Survey and
Classification of Storage Deduplication Systems* (ACM CSUR 2014).

## R2 cost model (and why it favours this design)

From <https://developers.cloudflare.com/r2/how-r2-works/> and
<https://developers.cloudflare.com/r2/pricing/>:

- **Egress is free.** Re-downloading costs time and CPU, never dollars.
- Storage **$0.015/GB-month** is the only charge that compounds — exactly
  where v1 is quadratic.
- Class A ops (PUT/LIST/COPY) $4.50/M; Class B (GET/HEAD) $0.36/M; deletes
  free; free tier 10 GB-month + 1M Class A + 10M Class B. 1,000 PUTs/day is
  ~$0.14/month: the classic "many small objects are expensive" objection
  mostly dissolves on R2.
- R2 is **strongly consistent** and supports conditional writes (already used
  by the v1 catalogue).

Consequences encoded in this design:

- per-file blobs + small manifests hit R2's cheap classes; storage approaches
  the theoretical floor (each unique file version stored once, ever);
- the one genuine op-cost trap is **LIST** (Class A): steady-state paths must
  learn remote state from manifest/pointer GETs, never by listing blobs. This
  is an invariant (I6 below), not a guideline;
- free egress makes fresh-machine bootstrap and per-blob fetches costless in
  dollars, removing the main reason other systems pack small objects.

## Design

### Object model and key layout

```text
<depot root or bucket>/
  aha-depot.json                                # marker (schema aha-depot/v2)
  blobs/v2/<sha256>.zst                         # one compressed file version, write-once
  machines/index.json                           # machine-namespace registry, conditional PUT
  machines/<machine_id>/manifests/<sha256>.json # one snapshot manifest, write-once
  machines/<machine_id>/latest                  # pointer {manifest_sha256}, conditional PUT
```

The **machines index** exists so pull can discover machine namespaces with a
single GET instead of a LIST (I6). It is appended to (conditional PUT with
retry) only on a machine's first-ever push.

A **manifest** is the deterministic JSON serialization of a snapshot:
machine identity, capture metadata, adapter versions, policy, and the full
file list — each entry carrying source, kind, relative/raw path, size, and
`blob_sha256`. The manifest's own SHA-256 is the **snapshot identity**;
`bundle_id` (a name that had to be policed for emptiness and uniqueness) is
deleted in favour of this fact that cannot be forged or collide. Manifests are
*logically full*: every snapshot lists the machine's complete state, so there
are no chains and no parent references to break.

The corpus table `bundles` becomes `snapshots(manifest_sha256, machine_id,
captured_at, ingested_at, manifest_json)`; `session_versions.bundle_id`
becomes `manifest_sha256`. Provenance ("which snapshots observed this file
version") is unchanged in shape.

### The three verbs

- **push** (today's `snapshot`): GET your machine's `latest` pointer and that
  manifest (a few KB); stat/hash local sources; diff against the parent
  manifest; PUT only blobs the parent doesn't list; PUT the new manifest; PUT
  the pointer (conditional). No corpus required — the parent manifest is the
  diff baseline (restic's parent-snapshot trick). A machine with a corpus may
  additionally use it to skip re-hashing (see capture cache).
- **pull** (today's depot-mode `ingest`): GET every machine's pointer +
  latest manifest; diff each manifest against the corpus
  (`session_versions` already records known `(machine, path, sha)` versions);
  fetch only unknown blobs; parse only fetched blobs (the shipped per-file
  parse skip already guarantees parse-once).
- **refresh** = push + pull, unchanged as the muscle-memory command. Because
  push derives its delta from the parent manifest and pull derives its
  fetches from the corpus, a steady-state refresh with no changes is a
  handful of small GETs and zero PUTs, zero parses, zero blob fetches.

### Invariants — by construction first

Per `docs/correctness-by-construction-spec.md`, each invariant names its
strongest available enforcement; runtime checks are a last resort and get an
explicit residual-risk test.

- **I1 — Blob immutability/dedup.** A blob key *is* the SHA-256 of its
  contents. Writes go through the shared CAS store's `ExistingOK` atomic
  write (extracted from today's corpus blob writer + `internal/fileutil`):
  identical content is one object by construction; differing content under
  one key is rejected on read by hash verification (residual: corruption —
  property-tested).
- **I2 — Publish ordering.** A manifest that references an unpublished blob
  must be unrepresentable. The publisher API is typestate-shaped: callers
  obtain a `BlobReceipt` only from a successful blob put, and
  `Manifest.Build` accepts file entries only paired with receipts (or proof
  the parent manifest listed the blob). The pointer PUT accepts only a
  `PublishedManifest` returned by the manifest put. A crash mid-push leaves
  unreferenced blobs (harmless, kept by I5) — never a dangling reference.
- **I3 — Per-machine write namespace.** A writer holds a
  `MachineDepot` handle whose key-construction is private and parameterized
  by its own validated machine ID; foreign-prefix keys cannot be formed. The
  contribute-only property (below) falls out of this type, and an
  operation-count test pins it.
- **I4 — Snapshot identity is content.** `ManifestSHA256` is an opaque type
  with a smart constructor (like `model.SHA256Hex`); manifest serialization
  is deterministic (sorted keys/entries, fixed encoding), property-tested:
  equal states ⇒ byte-equal manifests ⇒ equal identity.
- **I5 — Append-only, no GC.** The depot never deletes. v1 already proved
  this posture; v2 makes it policy: at $0.015/GB-month with O(unique-bytes)
  growth, keeping every file version ever is rational, and declining GC
  sidesteps the hardest operational problem in the restic/kopia family. The
  static network/mutation guards in `internal/testquality` extend to assert
  no delete calls exist in depot v2 production code.
- **I6 — No LIST on steady-state paths.** push/pull/refresh/status discover
  state via pointer + manifest GETs only. Enforced by counting-driver
  property tests (the `countingStateDepot` pattern already used for
  `state_sha256` no-fetch proofs). `depot verify` may LIST; it is explicitly
  the audit path.
- **I7 — Parse-once.** Already shipped and pinned
  (`TestUnchangedSessionFileSkipsReparseAcrossBundles`,
  `TestIdenticalBytesDifferentSessionStillParsed`): a session-file version
  proven present (same machine, source, paths, content hash, committed in
  the same transaction as its entries) is never re-parsed; byte-identical
  content under a different session/path always is.

### Capture cache (scan layer) — advisory only

A `(path, size, mtime_ns, inode) → sha256` cache (sidecar next to the config,
or a corpus table where one exists) lets push skip re-hashing unchanged
files. **The cache is a work-skipping hint, never a correctness input**: a
wiped cache means one slow full re-hash, not data loss; `--force` bypasses
it. Residual risks are tested explicitly, git-style: the racy-mtime window
(file modified within the cache-write timestamp's granularity is always
re-hashed) gets a property test; "changed without mtime change" is covered by
`--force` and documented. Without any cache (e.g. a corpus-less machine),
push re-hashes (reads, never parses, never uploads unchanged data) — correct,
merely slower.

### What gets deleted

The headline of "no backwards compatibility": v2 is plausibly **net-negative
in code**, because most v1 machinery exists to manage problems monolithic
bundles create.

| Deleted | Why it existed |
|---|---|
| Bundle budgets + `ValidateManifestBudgets` (the 2 GiB cliff) | monolithic artefact size |
| `ManifestStateSHA256`, `state_sha256` plumbing, `findDepotBundleWithSameState`, `SkipIfUnchanged` | cheap "did anything change?" — replaced by manifest diff, which also says *what* changed |
| Bundle-level duplicate detection (`recordBundleAttempt` dedup) | duplicates are unrepresentable under I1 |
| `PromoteBundle`, corpus bundle blobs, and their verify/prune plumbing | per-corpus copies of full-history bundles (`corpus prune-orphans` survives for file/image blobs orphaned by rolled-back ingests) |
| Catalogue shard merge/repair + `depot compact` | multi-writer catalogue reconciliation — replaced by per-machine pointers (I3) + conditional PUT |
| Deterministic `tar.zst` writer/reader in `internal/archive` (kept only behind `export`/`import`) | the bundle file format |
| `bundle_id` and its validity policing | naming — replaced by manifest SHA (I4) |

What stays untouched: redaction, the adapter read-only invariant, the
network boundary, search/read/incidents/mcp/serve, the corpus append-only
triggers, and the FTS layer.

### Command set and journeys

The daily journeys in `docs/user-journeys.md` (1–3, 5–8) are preserved
**verbatim**: `init → refresh → search/read`, automation flags, `mcp`,
`serve`, `incidents` do not change shape. Changes:

- `snapshot` and depot-mode `ingest` keep their names but become push/pull as
  above (docs should say push/pull out loud).
- **New: `export`** — materialize a portable v1-format `bundle.tar.zst` from
  a manifest + blobs, preserving journey 4's "hand someone one file".
  `ingest <bundle.tar.zst>` (import) continues to read that format; together
  they are the only place the v1 format survives, and the compatibility
  hedge for any pre-v2 depot.
- **New journey: bootstrap a new machine** (promoted to first-class in
  `user-journeys.md`): `aha init && aha refresh` against an existing depot =
  pointer GETs + manifest GETs + only-needed blob fetches + parse-once. Free
  egress makes this the multi-machine pitch made real.
- **Deleted commands:** `depot compact` (per-machine pointers replaced
  catalogue shards). `corpus prune-orphans` survives: file/image blobs from
  rolled-back ingests can still orphan. Net command count: −1 subcommand,
  +1 export.
- `depot verify` reworked: quick = pointers resolve to manifests, manifests
  well-formed; `--deep` = every referenced blob present with matching hash
  (many small GETs — Class B, cheap in dollars, slower than streaming one
  tar; that trade is accepted and documented).

### Contribute-only machines — preserved by construction

The journey "this machine only creates snapshots and puts them into the
depot, without downloading anyone else's data" is **strictly improved**:
push reads only the machine's *own* pointer + parent manifest (a few KB) and
writes only its own namespace — I3 makes reading a foreign prefix
inexpressible, and an operation-count test pins "push performs zero foreign
GETs and zero LISTs". Where v1's contribute-only machine re-uploaded its full
history every snapshot, v2 uploads only new blobs. No corpus is required;
the parent manifest is the baseline.

### Benefits and accepted disadvantages

Benefits: every axis O(delta) or O(unique-bytes) **by construction**; cliff
unrepresentable; storage at the theoretical floor; fresh-machine bootstrap
fast and free in egress; net less code with fewer invariants to defend;
per-file blobs open the door to future partial/lazy ingest (not built now,
not designed out).

Accepted disadvantages, with mitigations:

- **Rewrite risk of a battle-tested core.** Mitigated by the phase plan
  below: every phase lands suite-green behind failing tests first, and the
  v1 path keeps working until the cutover phase.
- **Partial-publish surface.** Mitigated by I2 (ordering unrepresentable) and
  crash-injection tests; worst case is orphan blobs, which I5 keeps anyway.
- **`verify --deep` is slower** (many GETs vs one stream). Accepted; audit
  path only.
- **Growing JSONL files still store one whole blob per observed version.**
  Accepted residual; the Filebeat-style base+appended-segments refinement
  can layer on later without a format break (new blob kind in the manifest),
  and is explicitly out of scope.
- **A bucket of small objects is less eyeball-able with generic S3 tools.**
  `export` reconstructs a single-file view on demand.

## Implementation plan — red-green-refactor, CbC at every step

Rules of engagement (per `agents.md`): no production code without a failing
test that demands it; each phase ends with the full suite green
(`make verify-quick` locally, `scripts/verify.sh ci` in CI); when a choice
exists between a runtime check and a construction-time guarantee, take the
construction-time guarantee and add a residual-risk test for whatever Go or
SQLite cannot make impossible.

**Phase 0 — lock the contract.**
*Red:* golden tests for the v2 key layout, marker, manifest JSON schema, and
deterministic serialization — written against fixtures before any driver
exists, failing because nothing produces them.
*Green:* the constants and codec.
*Refactor:* none.
This freezes the public contract pre-release exactly as the v1 spec froze
`bundles/v1/` (Hyrum's Law pre-empted while there are no users).

**Phase 1 — model types.**
*Red:* property tests for `ManifestSHA256` and `BlobKey` smart constructors
(invalid hex/length unconstructable), manifest codec round-trip
(decode∘encode = id), and determinism (permuted-input states serialize
byte-equal).
*Green:* opaque types in `internal/model` mirroring `SHA256Hex`/`Ref`.
*Refactor:* share validation with existing identity types.
CbC: snapshot identity (I4) now cannot be malformed anywhere downstream.

**Phase 2 — shared CAS blob store.**
*Red:* contract tests for a store used by *both* corpus and depot:
write-once `ExistingOK` semantics (the existing
`TestKnownFileBlobSkipsRecompression` generalizes), same-bytes ⇒ same-key
property, read-side hash verification rejecting corrupted blobs
(fuzz/bit-flip), atomicity under simulated crash (temp+rename, reusing
`internal/fileutil` guarantees).
*Green:* extract today's corpus `blobs/files/` writer into the shared
package.
*Refactor:* corpus blob writes move onto it; byte copies per datum drop from
five toward two.
CbC: I1 lives in exactly one place.

**Phase 3 — depot v2 drivers (local + R2).**
*Red:* shared driver contract tests (the existing local/R2-fake pattern):
publish ordering via the typestate API — a test that *fails to compile* is
the ideal red for I2, plus runtime crash-injection showing a half-push leaves
no dangling manifest/pointer; `MachineDepot` foreign-key inexpressibility
(compile-shape) plus counting-driver tests for I3/I6 (push: zero foreign
GETs, zero LISTs; pointer update: conditional PUT with retry-on-conflict,
reusing the v1 R2 conditional-write tests); `testquality` static guard
extended for I5 (no delete calls).
*Green:* v2 local + R2 drivers besides the v1 drivers (both live during the
bridge).
*Refactor:* none yet — deletion is Phase 7.

**Phase 4 — push (capture-as-diff).**
*Red:* property test "push twice with unchanged sources ⇒ second push PUTs
nothing and the pointer is unchanged"; "one new/grown file ⇒ exactly that
blob + one manifest + one pointer PUT" (operation counts); parent-manifest
diff correctness against randomized state mutations; capture-cache residual
tests (racy-mtime window always re-hashes; `--force` ignores the cache;
wiped cache changes operation counts, never the resulting manifest).
*Green:* push path in `internal/archive`→`internal/cli`, parent-manifest
baseline, advisory stat cache.
*Refactor:* capture loses its parse-for-entry-counts (entry counts move to
ingest-derived corpus facts), shrinking capture to discover+stat+hash.

**Phase 5 — pull (ingest anti-entropy).**
*Red:* operation-count properties: "corpus in sync ⇒ pull fetches pointers +
manifests and zero blobs"; "one unknown version ⇒ exactly one blob fetch and
one parse" (extending the shipped parse-once tests, I7); corpus schema red
tests for `snapshots(manifest_sha256, …)` and `session_versions`
re-keying; equivalence property: a corpus built by pulling a depot equals a
corpus built locally from the same sources (determinism end-to-end).
*Green:* manifest-diff pull; corpus DDL change (no users: corpora are
rebuildable from the depot by decree of the v1 spec — rebuild, don't
migrate).
*Refactor:* dedupe pull and local-import paths over the shared CAS.

**Phase 6 — CLI cutover, `export`, docs.**
*Red:* command-level golden/JSON-shape tests for the rewired
`refresh`/`snapshot`/`ingest`, the new `export` (export∘import round-trips a
manifest's full state through a v1 bundle file), and failing drift tests:
`architecture.html` (command count, package facts), `commands.md`
generation, `user-journeys.md` gains the bootstrap journey,
`depot-lifecycle.md` rewritten for pointer/manifest states.
*Green:* the rewiring and docs.
*Refactor:* none.

**Phase 7 — the deletion (pure refactor, suite stays green).**
Remove everything in the deletion table: v1 drivers, budgets, state
signatures, bundle dedup, `PromoteBundle`/`prune-orphans`/`compact`, catalogue
merge, `bundle_id`. Each removal is demanded by a test *changing or
disappearing first* where behaviour changes (e.g. `prune-orphans` golden
tests deleted in the same commit), and `testquality`'s debt inventory is
updated so nothing creeps back. The drift tests force `architecture.html`
and the docs to track the shrinkage.

**Acceptance properties (end-to-end, all operation-count or
equivalence-based, never wall-clock):**

1. steady-state `refresh` on M machines: O(M) small GETs, zero PUTs, zero
   blob fetches, zero parses;
2. day-with-one-new-session `refresh`: exactly the delta's blobs uploaded
   once fleet-wide, parsed once fleet-wide per machine corpus;
3. contribute-only push: zero reads outside the machine's own prefix, ever;
4. bootstrap-from-depot corpus ≡ locally-built corpus, byte-for-byte in all
   queryable facts;
5. no depot object is ever written twice with different content, and no
   depot object is ever deleted.

## Out of scope

- Chunking / rolling hashes / append-segment blobs for growing files
  (future refinement; manifest format leaves room via a blob-kind field).
- Garbage collection or retention (declined by policy, I5).
- Client-side encryption (unchanged from the v1 depot spec's reasoning).
- Any depot driver beyond local + R2.
- Compatibility machinery beyond `export`/`import` of v1 bundle files.
