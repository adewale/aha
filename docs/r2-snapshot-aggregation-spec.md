---
title: R2 Snapshot Aggregation Spec
kind: spec
created: 2026-05-23
updated: 2026-05-23
tags:
  - agents
  - session-history
  - snapshots
  - aggregation
  - depot
  - cloudflare-r2
  - object-storage
  - multi-machine
  - go
  - cli
  - specification
source_type: design
status: proposed
aliases:
  - R2 Snapshot Aggregation
  - Depot
  - aha depot
---

# R2 Snapshot Aggregation Spec

## Status and relationship to v1

This is a **proposed** design, not a locked v1 decision. It extends the v1
system in `docs/agent-history-aggregator-spec.md` by adding an **optional,
off-by-default** aggregation tier: a **depot** that can live on the local
filesystem (as today) or in a **private Cloudflare R2 bucket**. It deliberately
relaxes two v1 boundaries — "local-only, no network" (Guarantee 3 in
`docs/trust.md`) and "do not upload bundles" (the README privacy warning) — so
it is a v2-class capability that must stay off by default.

The v1 design already contains the seam that makes this clean:

> The bundle is the receipt. The corpus is the index.

Bundles are immutable, deterministic, and content-addressed by SHA-256 — exactly
the contract object storage wants. This spec routes those same bundle bytes
through a **depot** (local dir or R2) and keeps every analysis running against a
**local** SQLite + FTS corpus rebuilt from the aggregated bundles.

## Goal

Let many machines publish their snapshot bundles into one **shared depot**, and
let any authorized machine rebuild or update a **local** corpus from the union of
those bundles. The depot is the durable, shared, egress-free distribution point
for bundles; the local corpus stays the query engine for the analyses `aha`
already supports.

## Domain model (the implicit model, made explicit)

The tool's nouns and how they relate. The remote feature **adds nouns**
(destination, depot, catalog) but **no new verbs** — it generalizes the existing
pipeline so a bundle's home can be local or remote.

```text
                         ┌──────────┐  binds (default)   ┌──────────────────────┐
                         │ Machine  │ ─────────────────► │ Depot                │
                         │ id,label │                    │ = default Destination│
                         └────┬─────┘                    │ holds many Bundles   │
            owns │           │ produces                  │ has a Catalog        │
                 ▼           ▼                           └──────────┬───────────┘
        ┌──────────────┐   ┌───────────────────────┐               │ lives on
        │ Corpus       │   │ Bundle  (the Receipt)  │               ▼
        │ local index  │   │ immutable, sha256      │     ┌────────────────────────┐
        │ per machine  │   │ produced_at, machine   │     │ Destination            │
        └──────┬───────┘   │ described_by Manifest  │     │ (type, location)       │
   indexes │   │ rebuilt   │ contains many Files    │     │ type ∈ {local, r2}     │
           ▼   │  from     └───────────┬────────────┘     │ driver = transport     │
   ┌───────────────────┐               │ normalize to     └────────────────────────┘
   │ Session           │◄──────────────┘
   │ source, machine   │   ┌───────────────┐
   │ cwd, started_at   │   │ Source        │ adapter: pi | claude-code | codex
   │ has many Entries  │◄──│ discovered on │
   └─────────┬─────────┘   │   a Machine   │
             │ has         └───────────────┘
             ▼
   ┌───────────────────┐   ┌───────────────┐   ┌───────────────────────────────┐
   │ Entry             │──►│ Asset (image) │   │ Conflict                      │
   │ role, ts, raw_json│   └───────────────┘   │ recorded in a Corpus when     │
   │ entry_sha256      │                       │ ingest finds divergent entries│
   └───────────────────┘                       │ for the same session/entry id │
                                               └───────────────────────────────┘
```

| Entity | Identity | Lifetime | Scope |
|---|---|---|---|
| **Machine** | `machine_id` | stable | one per host |
| **Source** | adapter name (`pi`/`claude-code`/`codex`) | stable | per machine |
| **Session / Entry / Asset** | source-native or derived IDs | immutable once captured | per source |
| **Bundle (Receipt)** | `bundle_sha256` (+ `bundle_id`) | **immutable** | shared via depot |
| **Manifest** | embedded in bundle | immutable | per bundle |
| **Destination** | `(type, location)` | config | addressable anywhere |
| **Depot** | the default Destination | config | **one, shared** |
| **Catalog** | per-machine shards | append-mostly | one per depot |
| **Corpus** | the local DB | **derived, disposable, rebuildable** | **one per machine** |
| **Conflict** | `(session_key, entry_id)` | recorded at ingest | per corpus |

Two relationships carry the whole design:

- A **Depot is the default Destination.** "Destination" is the addressing
  primitive (`type` + `location`); "depot" is the role played by the one you
  standardised on. This mirrors restic's default repo vs. `--repo`, and git's
  `origin` vs. an explicit remote.
- A **Corpus is derived from Bundles.** Bundles are the source of truth; the
  corpus is a rebuildable local index. You can delete a corpus and rebuild it
  from the depot, never the reverse.

## Primitives and vocabulary

| Term | Meaning | Replaces / why |
|---|---|---|
| **bundle** / **receipt** | the immutable, content-addressed `tar.zst` snapshot | project's own metaphor: a dated, hash-verified record you file and never edit |
| **destination type** (driver) | transport implementation: `local`, `r2` | was the `Backend`/`BundleStore` interface; "driver" reads cleaner and matches restic backends |
| **destination** | a `(type, location)` address users can name, e.g. `r2:aha-depot` or `~/agent-depot` | new primitive; collapses type+location into one token like restic's repo URI |
| **depot** | the **default destination** — the standing aggregation point | renames the v1 "aggregation point"/`bundle_out_dir`; implies central storage without implying source control |
| **catalog** | the bundle listing in a depot, sharded per machine | replaces "index" (which already means the corpus/FTS) |
| **corpus** | the local SQLite + FTS index built from bundles | unchanged |
| **`BundleRef`** | an in-code reference to a bundle | replaces `BundleObject` (dropped the S3 "object" leak; rhymes with the existing `HitRef`) |

Retired from the earlier draft: **`remote`** (asymmetric — a local folder is a
destination too), **`index/`** key prefix (collided with the corpus), and
**`push`/`pull`/`sync`** verbs (`sync` collides head-on with the locked
"Live sync: No" non-goal; the others carry git baggage).

## Architecture and flows

Per-machine pipeline — only `snapshot`/`ingest` cross the depot boundary;
everything analytical stays on the local corpus:

```text
  LOCAL SOURCES                 DEPOT (local dir or r2:aha-depot)        LOCAL CORPUS
  ~/.pi  ~/.claude  ~/.codex          [ shared bundle pool ]            [ per-machine index ]
        │ discover+parse                                                       
        ▼                                                                      
   aha snapshot ── bundle(receipt) ──push──►  bundles/v1/<sha>.tar.zst          
                                              catalog/v1/<machine>.json         
                                                      │                         
                                                      └──pull── aha ingest ──► corpus.db
                                                                               + FTS + blobs
                                                                                    │
                                                              search · read · status · conflicts
                                                                         (local, offline)
```

Three machines, one depot — the centerpiece. The depot is a single shared pool
of immutable bundles; each machine keeps its **own** local corpus that converges
as it ingests:

```text
        ┌──────── ade-mbp ────────┐     ┌──────── work-mac ───────┐     ┌──────── linux-box ──────┐
        │ sources → snapshot → A  │     │ sources → snapshot → B  │     │ sources → snapshot → C  │
        │ corpus(ade) ◄─ ingest   │     │ corpus(work) ◄─ ingest  │     │ corpus(lin) ◄─ ingest   │
        └───────────┬──────┬──────┘     └──────┬───────────┬──────┘     └──────┬───────────┬──────┘
              push A │      │ pull          push│ B         │ pull       push C │           │ pull
                     ▼      ▲                   ▼           ▲                   ▼           ▲
                  ┌────────────────────────────────────────────────────────────────────────┐
                  │                  DEPOT   r2:aha-depot   [ SHARED ]                        │
                  │      bundles/v1/A  bundles/v1/B  bundles/v1/C  bundles/v1/A2 ...          │
                  │      catalog/v1/ade-mbp.json  …/work-mac.json  …/linux-box.json           │
                  └────────────────────────────────────────────────────────────────────────┘

  One bundle pool, three independent indexes. Convergence is pull-driven and eventual:
  a machine only knows the bundles it has ingested. Nobody ever queries the depot to search.
```

## Opinionated defaults

**R2 depot bucket: `aha-depot`.** No suffix, no machine name, no random token.
Lowercase, 9 chars, a valid R2/S3 bucket name, one per account.

This must be opinionated for **correctness, not tidiness**: a depot only
aggregates if every machine lands in the **same** bucket. A per-machine or
randomised default would silently create three private buckets that never merge —
the opposite of the feature. So the default must be stable and identical
everywhere; a second depot is an explicit override (`--depot r2:aha-archive`).

Local depot default `~/.aha/depot`, giving:

```text
~/.aha/
  depot/                          # the depot (bundle pool) — default destination
    bundles/v1/<bundle_sha>.tar.zst
    catalog/v1/<machine>.json     # per-machine shard: cheap listing, no write contention
  corpus.db                       # the corpus (local index) — separate subsystem
  blobs/                          # corpus's own content-addressed blobs
```

The `v1/` segment versions the key layout. Key scheme, catalog schema, manifest
schema, and the bundle content hash are **explicit, versioned public contracts**
(see Hyrum's Law), not incidental internals.

## Depot vs. corpus

Two subsystems with different jobs, lifetimes, and locations:

| | **Depot** | **Corpus** |
|---|---|---|
| Holds | immutable, content-addressed bundles (receipts) | SQLite + FTS index built from bundles |
| Scope | **one, shared** across machines | **one per machine**, local |
| Role | durable **source of truth** | **derived**, disposable, rebuildable |
| Written by | `snapshot` | `ingest` |
| Read by | `ingest` | `search` / `read` / `status` / `conflicts` |
| Needs network | yes (if R2) | no — always local |

Mental model in brand-neutral terms: the depot is a **shared folder of immutable
receipts**; the corpus is a **locally-built search index** over them — like macOS
Spotlight, Windows Search, or `locate`'s database on Linux: derived from your
files, rebuildable from scratch, and never the source of truth. Nobody "searches
the shared folder" — you search your local index, which you keep fed from the
folder. That is why there is **no shared remote corpus**: querying R2 per search
would mean many round-trips, lose offline use, and force a single-writer SQLite
file in a bucket — all rejected for conceptual integrity (see audit). **Depot =
the bundle aggregation point, full stop. The corpus stays local.**

## Commands in the depot model

Organizing rule: **only `snapshot` (write), `ingest` (read), `refresh` (both),
and the `depot`/`doctor` diagnostics touch the depot. Everything analytical reads
the local corpus only and works offline.** The depot is implicit (you chose it
once); any depot-touching command can override it for one invocation with
`--depot <destination>`.

| Command | Depot | Corpus | Network | Behavior |
|---|---|---|---|---|
| `aha init` | — | — | no | scaffolds config; records your depot choice |
| `aha depot init <dest>` | create/bind | — | yes (r2) | creates or connects the depot, writes it to config |
| `aha snapshot` | **write** | — | yes (r2) | builds a bundle, pushes it to the depot |
| `aha ingest` | **read** | write | yes (r2) | pulls bundles new to you, merges into local corpus |
| `aha refresh` | **read+write** | write | yes (r2) | `snapshot`→depot, then `ingest`←depot |
| `aha depot ls` | read catalog | — | yes (r2) | lists what is in the shared pool |
| `aha depot verify` | read | — | yes (r2) | integrity: re-hash objects, catalog↔bucket agree |
| `aha search` | — | read | **no** | queries your local corpus |
| `aha read` | — | read | **no** | retrieves full context/blob from your local corpus |
| `aha status` | optional | read | no by default | local corpus health; `--depot` adds a "behind by N bundles" line |
| `aha conflicts` | — | read | **no** | lists quarantined merge conflicts in your corpus |
| `aha doctor` | check | check | yes (r2) | diagnostics incl. depot reachability + credentials |

Notable behaviors:

- **`read` never needs the depot, even though the data came from it.** `ingest`
  copies the bytes it needs into the corpus's own blob store, so `read` (incl.
  image reconstruction) works offline forever.
- **`conflicts` matters more with multiple machines.** Quarantined conflicts are
  mostly a cross-machine phenomenon (same session id, divergent entries); the
  command is unchanged, but the depot is where conflicting bundles arrive from.
- **`status` stays local-and-fast by default;** `--depot` cheaply diffs your
  corpus's known bundle set against the catalog and reports how far behind you
  are.
- **`depot ls` vs `status`** are the two halves: what is in the shared pool vs.
  what is in your local index; the gap is what the next `refresh` pulls.
- **`--depot` is inert for `search`/`read`/`conflicts`** (nothing remote to point
  at); `status --depot` is the one analytical command that uses it.

## Typical usage: three machines

Machines `ade-mbp`, `work-mac`, `linux-box`, all sharing `r2:aha-depot`.

One-time setup (per machine); the first creates the bucket, the others connect;
credentials come from the environment, never config:

```console
ade-mbp$ export R2_ACCESS_KEY_ID=... R2_SECRET_ACCESS_KEY=...
ade-mbp$ aha depot init r2                 # opinionated default bucket "aha-depot"
created depot r2:aha-depot  (account ...e91)

work-mac$  aha depot init r2:aha-depot     # bucket exists → connect
linux-box$ aha depot init r2:aha-depot
```

Daily use is just `aha refresh` on each machine — it publishes your new snapshot
and absorbs everyone else's:

```console
ade-mbp$ aha refresh
snapshot → bundle A  (machine=ade-mbp, 41 sessions)
push     → r2:aha-depot  (1 new, 0 skipped)
ingest   ← r2:aha-depot  (1 new bundle)
corpus: sessions=41 entries=5120 messages=3360 conflicts=0
```

State as the day unfolds — "Depot" is the single shared bucket; each "corpus" is
a separate local database:

| When | Command | Depot (R2), shared | That machine's corpus |
|---|---|---|---|
| T1 | `ade-mbp$ aha refresh` | `A` | ade-mbp: `A` |
| T2 | `work-mac$ aha refresh` | `A, B` | work-mac: `A, B` |
| T3 | `linux-box$ aha refresh` | `A, B, C` | linux-box: `A, B, C` |
| T4 | `ade-mbp$ aha refresh` | `A, B, C, A2` | ade-mbp: `A, B, C, A2` |

What this makes concrete:

- **One bundle pool, many indexes.** After T4 the depot holds `{A, B, C, A2}` —
  one deduped, content-addressed copy. Each `corpus.db` is a separate file that
  converges in content as it ingests.
- **Convergence is pull-driven and eventual.** Right after T4, `work-mac`'s
  corpus is still `{A, B}` until its next `refresh`.
- **Search is always local and offline.** `aha search` hits only the local
  corpus.
- **Idempotent and safe to over-run.** Re-ingesting `A` anywhere is a no-op;
  back-to-back `refresh` changes nothing the second time.
- **The corpus is disposable; the depot is durable truth.** A dead machine is
  rebuilt from the depot:

```console
new-box$ aha depot init r2:aha-depot
new-box$ aha ingest                 # pull everything; full history reconstructed
```

Overrides, when needed:

```console
ade-mbp$ aha search "needle"                       # local corpus, offline
ade-mbp$ aha ingest --depot r2:aha-archive         # pull from a different depot
ade-mbp$ aha snapshot --depot local:/Volumes/usb/depot
```

## Cloudflare R2 facts this design relies on

From `https://developers.cloudflare.com/r2/how-r2-works/` and the R2 limits docs:

- **S3-compatible API**, SigV4 auth, region `auto`; the S3 API is the data plane.
- **No egress fees** — pulling the whole pool to many machines is not metered by
  bytes; operations (Class A writes/lists, Class B reads) are metered.
- **Strong consistency** (read-after-write, list-after-write) — "push then
  another machine pulls" is reliable without convergence hacks.
- **Encrypted at rest by default**; per-bucket and scoped API tokens.
- Limits: object **5 TiB**; single-part PUT **5 GiB**; multipart up to **10,000
  parts / 4.995 TiB**; key length **1,024 bytes**; **unlimited** objects/bytes per
  bucket; up to **1,000,000 buckets/account**; S3/Workers API recommended over
  the REST API for high object counts (REST cap ~**1,200 req / 5 min**);
  same-object concurrent writes throttled ~1/s.

Two consequences: the current `MaxBundleBytes = 2 GiB` cap fits under the **5 GiB
single-PUT** limit (no multipart needed unless the cap is raised); and bundles
already aggregate many tiny session files into one object, keeping object counts
and per-op costs low.

## What must change, be added, or be removed

### Add

- `internal/depot` package: a **destination driver** interface with `localFS`
  and `r2` implementations, plus a minimal S3 **SigV4** signer.
  **Recommendation:** a small hand-rolled SigV4 client over `net/http` + stdlib
  `crypto/sha256`/`crypto/hmac` to preserve `aha`'s pure-Go, CGO-free,
  minimal-dependency ethos; alternative `minio-go`. Either way the signer needs
  golden SigV4 vector tests.
- `depot` config block (type + location, off by default), `aha depot
  init/ls/verify`, and `--depot` on the depot-touching commands.
- Per-machine catalog shard reader/writer; content-addressed `bundles/v1/<sha>`
  keys.
- **Client-side encryption by default when a depot is remote** (restic/kopia
  lesson — see Security).
- Credential loading from env / OS keychain / `0600` file — never committed,
  never written into manifests or bundles.

### Change

- **Re-scope the no-network guarantee; don't delete it.**
  `internal/cli/security_static_test.go` currently fails the build if **any**
  non-test file under `cmd/`/`internal/` imports `net`, `net/http`, `net/url`,
  `net/rpc`. Its comment already anticipates this: *"update docs/trust.md if v1
  network behavior changes."* Change it to **exempt only `internal/depot`** and
  assert every other package stays net-free (alternative: gate `internal/depot`
  behind `//go:build r2`). The local-only guarantee stays mechanically enforced
  for the core.
- `docs/trust.md`: reframe Guarantee 3 to "local-only by default; depot is
  opt-in, private, scoped, integrity-checked, encrypted client-side," and add a
  depot-security guarantee.
- README privacy warning: uploading **unredacted** bundles is real exposure even
  to a private bucket; recommend v2 redaction and/or client-side encryption
  before enabling a remote depot.
- `writeSnapshot`/`cmdIngest`/`cmdRefresh` route bundle bytes through the
  selected destination driver.

### Remove / explicitly avoid

- Do **not** weaken the local-first default; the depot is opt-in.
- Do **not** store `corpus.db` in R2 as a shared writable query engine (unsafe
  multi-writer SQLite; contradicts "SQLite is the engine, not a cache").
- Avoid querying bundles in place over the network for analyses.
- Retire the words `remote`, `index` (as a key prefix), and the
  `push`/`pull`/`sync` verbs.

## Trade-offs

| Dimension | Local-only v1 (today) | With a depot (proposed) |
|---|---|---|
| Trust model | "Everything stays on your machine." Mechanically proven no-network. | Histories leave the machine; no-network becomes "local-only by default." Biggest cost. |
| Secrets | Bundles never uploaded; v1 does not redact. | Unredacted content lands in third-party storage → demands redaction or client-side encryption. |
| Setup | Zero credentials. | Cloudflare account, bucket, scoped tokens, key management. |
| Dependencies | Pure-Go, tiny dep set. | Adds an S3 client / SigV4 (dep weight or hand-rolled + tested). |
| Multi-machine | Manual bundle copy + `ingest`. | One shared depot; `refresh` converges automatically. |
| Durability | One disk. | Cloudflare-managed redundancy. |
| Offline | Always. | Analyses still local/offline; only snapshot-push and ingest-pull need network. |
| Failure surface | Filesystem only. | + auth, throttling, partial uploads, partitions. |

Net: trades `aha`'s strongest privacy property for durable, low-friction,
egress-free multi-machine aggregation — acceptable only as an explicit,
off-by-default opt-in paired with redaction or encryption.

## Performance and scalability concerns

- **Operation cost dominates, not byte cost.** No egress fees, but each push is a
  Class A `PUT`, each catalog/`LIST` is Class A, each pull is a Class B `GET`.
  Keep bundles aggregating many session files into one object; never fan out to
  per-session objects.
- **Avoid full-bucket `LIST` on every sync.** Per-machine catalog shards make
  sync cost proportional to **machines**, not total bundles; full `LIST` is a
  repair path only.
- **Pull is N round trips.** Parallelize downloads with bounded concurrency and
  stream into the existing staging path; the SQLite write
  (`db.SetMaxOpenConns(1)`) is the serialization point, so overlap network with
  parse.
- **Bundle size vs. PUT limit.** 2 GiB cap < 5 GiB single PUT today; raising the
  cap past 5 GiB forces multipart.
- **Same-object write throttle (~1/s)** is irrelevant — content-addressed keys
  are unique and existing objects are skipped via `Stat`.
- **Query performance does not improve.** The depot aggregates *bundles*;
  analyses still run on local SQLite. A very large aggregate corpus is an
  FTS/SQLite scaling problem independent of R2.
- **Strong consistency simplifies sync;** use the S3/Workers data plane (not the
  REST cap), honor HTTP 429 with bounded backoff, and cap concurrency.

## Security: how depot access is secured

The bucket is **private and treated as holding unredacted secrets.**

1. **Private bucket only** — no public bucket, no `r2.dev` URL, no public custom
   domain.
2. **Scoped, least-privilege API tokens — never account-scoped.** Per-machine
   write tokens for snapshot; read-only tokens for pull-only/analysis hosts; one
   token per machine so a lost laptop is revoked without rotating everyone.
3. **Credentials never in repo or bundles.** Loaded from env, OS keychain, or a
   `0600` file outside the corpus/depot trees. `aha` must assert credentials
   never appear in `manifest.json`, bundle bytes, `--json`, receipts, or config.
4. **TLS-only transport**, SigV4 signing.
5. **Integrity verification on download** — already enforced: ingest re-hashes
   every file against the manifest and the bundle SHA against the key; tampered
   or truncated objects are rejected before promotion.
6. **Client-side encryption by default for remote depots (restic/kopia model).**
   Encrypt bundle bytes before upload so Cloudflare sees only ciphertext; the key
   is user-held, never in the bucket. This is a stronger answer to the
   no-redaction problem than redaction alone. Note the dedup tension in the
   audit.
7. **Tamper resistance / retention** — object versioning or bucket lock so
   bundles cannot be silently overwritten or deleted.
8. **Auditability** — R2 event notifications / access logs.

## Prior art

We are reinventing a well-trodden shape: content-addressed immutable snapshots to
an object-store backend with a separate, rebuildable index.

| Tool | What we borrow | Difference / lesson |
|---|---|---|
| **restic** | `repository`/`snapshot`/`index`/`pack`/`backend` vocabulary; S3 backend; default-repo vs `--repo`; `check`/`prune` lifecycle | encrypts client-side **by default** (adopt); its "index" is blob-location, not full-text — our corpus is richer |
| **kopia** | object-store-native, **multi-client-safe** shared repository; clean backend/index/manifest layering | closest operational analog to "many machines, one depot"; our content-addressed unique keys give the same write-safety |
| **borg** | dedup + client-side encryption ideas | **not** object-store-native (needs FS/SSH, in-place mutation) — the example of what *not* to require |
| **perkeep** | content-addressed blobs + **separate, rebuildable search index** + multi-backend sync | closest *architectural* analog to "bundle = receipt, corpus = index"; validates keeping search local and the depot a pure blob store |
| **DVC / git-annex / OCI** | `push`/`pull` of content-addressed blobs to a `remote` | precedent for the verbs; we still prefer generalizing `snapshot`/`ingest` over minting them |
| **rclone** | object-store transfer | its `sync` is a destructive mirror — a cautionary tale for the word "sync" |
| `geekmuse/chronicle` | multi-machine session sync, path canonicalization | syncs **live files** (Git backend, CRDT); we replicate **immutable bundles** on demand |
| `badlogic/pi-share-hf` | incremental collection, **redaction, review, upload** | the exact snapshot→redact→upload shape, plus the redaction we flag as a prerequisite |

Key transferable lessons: **client-side encryption by default** (restic/kopia)
beats relying on redaction; **the index is derived from blobs** (perkeep) — keep
search local and the depot a pure receipt pool.

## Verification: how we verify the new functionality

Grounded in the installed `testing-best-practices` skill (real objects over
mocks, table-driven, golden files, property tests, regression-test-first,
contract tests for external APIs, both-directions for security, no live network
in unit tests):

- **One destination-driver contract suite, run against every driver.** The same
  table-driven behavior tests run against `localFS`, an in-process S3 fake
  (`gofakes3`/in-memory), and — behind a `//go:build integration` tag with env
  creds — a real R2 bucket. Differential testing: all drivers satisfy identical
  observable behavior.
- **Use a real S3-compatible server, not hand mocks.** `gofakes3` in-process (or
  MinIO) gives real request/response + SigV4 with **no live network**.
- **SigV4 golden vectors** — sign known requests against AWS's published vectors
  (contract test that catches signer drift).
- **Roundtrip property:** `Get(Put(x)) == x` and the bytes hash to the expected
  SHA.
- **Idempotency:** pushing the same bundle twice → one object; pull+ingest twice
  → zero new rows (reuse existing ingest-idempotence tests through the depot).
- **Integrity regression (test-first):** mutate one byte of a stored object →
  pull/ingest rejects it on SHA mismatch and never promotes it.
- **Sync delta correctness:** assert the *exact* key set pulled equals
  `catalog − corpus`, not just "non-empty."
- **Multi-machine end-to-end:** machines A and B push distinct bundles through
  the fake depot; C ingests and `search` returns hits from both with `--machine`
  filtering.
- **Encryption (default for remote):** `decrypt(encrypt(x)) == x`; ciphertext ≠
  plaintext; wrong key fails closed; both directions asserted.
- **Security, both directions:** credentials authenticate **and** never appear in
  any emitted artifact.
- **Throttling/backoff:** fake depot returns HTTP 429 → bounded backoff with an
  injected clock (no real sleeps).
- **Doc-sync:** new commands/flags/config keys validated against the registry and
  config struct (`internal/cli/docs_test.go`, `flag_metadata_sync_test.go`).
- **No unconditional skips:** real-R2 tests live behind a build tag with a
  documented rationale.

## Verification: how we confirm we did not break existing functionality

- **Whole existing suite stays green:** `go test ./...`, `-race`, `go vet`, parser
  fuzz, determinism tests, and **all golden files unchanged** with the depot
  disabled. Bundle bytes and manifests are byte-identical — the depot only
  changes transport.
- **No-network guarantee stays mechanically enforced for the core.** The reworked
  static test still fails the build if any package other than `internal/depot`
  imports a network package; add it test-first before writing the `r2` driver.
- **Depot-off path is provably inert:** with no depot configured,
  `snapshot`/`ingest`/`refresh`/`search`/`read`/`status`/`conflicts` take the
  local path and produce identical output (golden + characterization).
- **Trust Guarantees 1 and 2 untouched:** snapshot read-only behavior and
  immutable-bundle ingest are unaffected; their tests pass unchanged.
- **Bundle-format characterization:** existing v1 bundles round-trip through the
  drivers and ingest identically.
- **Performance non-regression:** large-corpus ingest/parse benchmarks unchanged
  with the depot off.

## Hyrum's Law implications

> With a sufficient number of users of an API, it does not matter what you
> promise in the contract: all observable behaviors of your system will be
> depended on by somebody.

A shared depot vastly widens *observable* behavior, so things `aha` treats as
internal become de facto contracts:

- **Object key layout.** Once anyone scripts `aws s3 ls bundles/v1/…` or writes a
  lifecycle rule, the layout is load-bearing. Mitigation: versioned `bundles/v1/`
  and `catalog/v1/` prefixes, golden-tested key/catalog shapes, documented
  migration before any change.
- **Bundle filename, manifest schema, catalog schema.** Directly fetchable
  bundles mean external tools parse them; `omitempty`/"internal" fields will be
  depended on. Treat the bundle schema (`agent-session-snapshot-bundle/v1`) and
  catalog schema as real versioned contracts.
- **The content hash is a contract.** Determinism + content addressing means
  consumers pin and dedup on `bundle_sha256`; any change to zstd level, tar
  metadata, or manifest field order breaks dedup and external pins. The existing
  determinism tests must guard this harder.
- **Strong consistency.** "Push then immediately pull" will be relied upon;
  document it as the assumed model.
- **JSON output and throttle/retry behavior.** New `--json` depot fields and even
  observable retry timing / HTTP 429 surfacing will be scripted; once present
  they are effectively unremovable.

Mitigations, consistent with `aha`'s existing discipline: prefer **explicit
versioned contracts** over "this is internal" labels, golden-test every
observable surface (keys, catalog, manifest, hashes, JSON), and ship migration
paths rather than silent breaks.

## Conceptual integrity audit

Judged against Brooks's standard — the system should reflect **one** set of
design ideas, as if from a single mind, rather than good-but-uncoordinated
additions.

**Where it holds (strong):**

- **One mental model, generalized — not a bolted-on mode.** The feature adds
  nouns (destination, depot, catalog) but **no new verbs**: `snapshot` still
  makes a receipt, `ingest` still builds the index, now location-agnostic via
  `--depot`. The tool reads as "the same `aha`, with the depot able to live in
  R2," which is the signature of conceptual integrity.
- **Clean three-layer separation** — destination driver (transport) / depot
  (logical store) / corpus (index) — mirrors restic (backend/repo) and perkeep
  (blobs/index). Principled, not ad hoc.
- **Vocabulary is coherent and each word means one thing.** Retiring `remote`
  (asymmetric), `index` (collided with the corpus), and `BundleObject` (S3 leak)
  removed the cracks the earlier draft had. `depot` vs `destination` is a
  justified role-vs-primitive pair (like git `origin` vs a remote), not
  redundancy.
- **Defaults preserved.** Depot off by default; all v1 behavior, output, and
  trust guarantees are unchanged — the relaxation is explicit and bounded.

**Genuine tensions (must be decided, not hidden):**

1. **Content-addressing vs. client-side encryption — the sharpest tension.**
   Two core ideas partially conflict: dedup by *plaintext* SHA, and zero-knowledge
   encryption. Random-nonce encryption breaks cross-machine dedup and decouples
   the stored key from the plaintext hash; convergent encryption preserves dedup
   but leaks plaintext equality (a real confidentiality weakening). **Verdict:**
   default to per-depot authenticated encryption (not convergent), carry the
   plaintext `bundle_sha256` in encrypted catalog/metadata, and accept that two
   machines holding an identical bundle store two ciphertexts — rare, since
   bundles are per-machine. This is the one place the model is not perfectly
   clean; it must be a stated, threat-model-driven decision.
2. **"Live sync: No" non-goal vs. `refresh` doing push+pull.** `refresh`
   replicates **discrete immutable bundles on demand** — `git fetch`-like, not
   Dropbox-like. It does not continuously sync or mutate live session files, so
   it honors the non-goal's spirit. The spec must say this explicitly and keep
   the word "sync" off the command surface (done).
3. **"Everything stays on your machine" vs. the whole feature.** The biggest
   shift, resolved by opt-in + off-by-default + encryption-by-default +
   strengthened warnings. Integrity is preserved *because* the default is
   unchanged and the relaxation is explicit and bounded — not an incoherent
   reversal.

**Minor warts (acceptable, noted not hidden):**

- `--depot` is meaningful for depot-touching commands but inert for
  `search`/`read`/`conflicts`; `status --depot` is a normally-local command that
  optionally reaches the network. Acceptable with clear help text.
- Two `init` verbs (`init`, `depot init`) — mirrors git `init` vs `remote add`.

**Overall:** high conceptual integrity. The feature extends the existing pipeline
rather than paralleling it; the one unavoidable tension (dedup vs. encryption) is
explicit, and the two posture relaxations are deliberate and bounded.

## Rejected and deferred alternatives

- **`corpus.db` in R2 as a shared query engine** — rejected: unsafe multi-writer
  SQLite; contradicts "SQLite is the engine, not a cache."
- **Query bundles in place over the network per analysis** — rejected: many
  round-trips, loses offline use, FTS, determinism.
- **Publish a read-only prebuilt `corpus.db` snapshot to the depot** for fast
  bootstrap — deferred: a single-writer host publishes an immutable, versioned,
  read-only corpus snapshot others download to seed before incremental `ingest`.
  A cache/replica, never the multi-writer source of truth.
- **Per-session objects instead of bundles** — rejected: explodes object/op
  counts and discards the deterministic, content-addressed receipt.
- **`push`/`pull`/`sync` verbs** — rejected: `sync` collides with a non-goal; the
  others carry git baggage. Generalizing `snapshot`/`ingest` via `--depot` keeps
  the bundle→corpus seam visible.

## Definition of done for this feature

- Depot off by default; all v1 behavior, output, golden files, and trust
  guarantees unchanged.
- `internal/depot` is the only package importing a network package, enforced by
  the reworked static test.
- `snapshot`/`ingest`/`refresh` + `aha depot init/ls/verify` work through the
  destination-driver contract suite against local FS and an in-process S3 fake;
  real-R2 tests exist behind a build tag.
- Integrity verification rejects tampered/truncated objects (regression test).
- Client-side encryption on by default for remote depots; roundtrip + wrong-key
  tests pass.
- Credentials never appear in any emitted artifact (security test, both
  directions).
- `docs/trust.md` and README state the opt-in depot posture and the
  unredacted-upload warning; doc-sync covers new commands/flags/config.
- Key layout, catalog schema, manifest schema, and content-hash determinism are
  versioned and golden-tested as public contracts.
- The conceptual-integrity tensions above are explicitly decided (esp. dedup vs.
  encryption), not left implicit.
