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

The v1 design already contains the seam that makes this clean. The v1 spec ends:

> The bundle is the receipt. The corpus is the index.

That line motivates the design, but to keep the vocabulary clean this spec uses
exactly one name per entity: the snapshot artifact is always a **bundle**
("receipt" in v1 refers to the small `.receipt.json` sidecar that `snapshot`
writes — a different artifact), and the searchable database is always the
**corpus** (its full-text index is a component *inside* the corpus, not a synonym
for it).

Bundles are immutable, deterministic, and content-addressed by SHA-256 — exactly
the contract object storage wants. This spec routes those same bundle bytes
through a **depot** (local dir or R2) and keeps every analysis running against a
**local** corpus rebuilt from the aggregated bundles.

## Goal

Let many machines publish their snapshot bundles into one **shared depot**, and
let any authorized machine rebuild or update a **local** corpus from the union of
those bundles. The depot is the durable, shared, egress-free distribution point
for bundles; the local corpus stays the query engine for the analyses `aha`
already supports.

## Guiding principle: data loss outweighs data theft

For an agent-history archive, **losing the history is worse than someone reading
it.** This principle drives several decisions below: encryption is **optional and
off by default** (a mandatory key is itself a data-loss vector — lose the key,
lose every bundle); the depot favors **immutability, versioning, and
anti-deletion** over confidentiality hardening; and the corpus is always
**rebuildable from the depot**. Confidentiality is handled by a private bucket,
TLS, scoped tokens, and R2's at-rest encryption — defenses that cannot orphan
your data.

## Backwards compatibility

`aha` has **no users yet** — a one-time luxury. We therefore **lock the public
contracts now** (the `bundles/v1/` key layout, the `aha-depot/v1` marker, the
`aha-depot-catalog/v1` and manifest schemas, the bundle content hash, `--json`
shapes, and CLI flags) and freeze them with the golden/determinism tests below,
while **changing defaults and retiring flags freely, without migration shims**,
because nothing depends on current behavior. The concrete default/flag changes
this enables are listed under "What must change" — notably relocating the default
bundle store to `~/.aha/depot` and retiring `--out` in favor of `--depot`.
Hyrum's Law (below) thus becomes a **future** constraint we get to pre-empt, not
a present one to unwind. The discipline is to pay the contract-locking cost once,
before the first user.

## Domain model

The tool's nouns and how they relate. The remote feature **adds nouns** (depot,
catalog) but **no new verbs** — it generalizes the existing pipeline so a
bundle's home can be local or remote. Every entity below has exactly one name.

```text
                         ┌──────────┐  binds (default)   ┌──────────────────────────┐
                         │ Machine  │ ─────────────────► │ Depot                    │
                         │ id,label │                    │ addr = type:location     │
                         └────┬─────┘                    │ type ∈ {local, r2}       │
            owns │           │ produces                  │ holds many Bundles       │
                 ▼           ▼                           │ has a Catalog            │
        ┌──────────────┐   ┌───────────────────────┐    └────────────┬─────────────┘
        │ Corpus       │   │ Bundle                 │                 │ stores
        │ SQLite + FTS │   │ immutable, sha256      │◄────────────────┘
        │ per machine  │   │ produced_at, machine   │
        └──────┬───────┘   │ described_by Manifest  │
   indexes │   │ rebuilt   │ contains many Files    │
           ▼   │  from     └───────────┬────────────┘
   ┌───────────────────┐               │ normalize to
   │ Session           │◄──────────────┘
   │ source, machine   │   ┌───────────────┐
   │ cwd, started_at   │◄──│ Source        │ adapter: pi | claude-code | codex
   │ has many Entries  │   │ discovered on │
   └─────────┬─────────┘   │   a Machine   │
             │ has         └───────────────┘
             ▼
   ┌───────────────────┐   ┌────────────────────┐   ┌───────────────────────────────┐
   │ Entry             │──►│ Asset (e.g. image) │   │ Conflict                      │
   │ role, ts, raw_json│   └────────────────────┘   │ recorded in a Corpus when     │
   │ entry_sha256      │                            │ ingest finds divergent entries│
   └───────────────────┘                            │ for the same session/entry id │
                                                    └───────────────────────────────┘
```

| Entity | Identity | Lifetime | Scope |
|---|---|---|---|
| **Machine** | `machine_id` | stable | one per host |
| **Source** | adapter name (`pi`/`claude-code`/`codex`) | stable | per machine |
| **Session / Entry / Asset** | source-native or derived IDs | immutable once captured | per source |
| **Bundle** | `bundle_sha256` (+ `bundle_id`) | **immutable** | shared via depot |
| **Manifest** | embedded in bundle | immutable | per bundle |
| **Depot** | address `type:location` | config | **one, shared** (default + `--depot` override) |
| **Catalog** | per-machine shards | append-mostly | one per depot |
| **Corpus** | the local DB | **derived, disposable, rebuildable** | **one per machine** |
| **Conflict** | `(session_key, entry_id)` | recorded at ingest | per corpus |

Two relationships carry the whole design:

- A **depot is addressed as `type:location`** (`r2:aha-depot`, `local:~/agent-depot`).
  You configure one as your default; `--depot` points any depot-touching command
  at a different one for a single invocation. (Like restic's default repo vs.
  `--repo`, or git's `origin` vs. an explicit remote.) There is no separate
  "destination" noun — a depot *is* the addressable store.
- A **corpus is derived from bundles.** Bundles are the source of truth; the
  corpus is a rebuildable local database. You can delete a corpus and rebuild it
  from the depot, never the reverse.

## Primitives and vocabulary

| Term | Meaning | Notes |
|---|---|---|
| **bundle** | the immutable, content-addressed `tar.zst` snapshot | canonical name; **not** called a "receipt" — that is the existing `.receipt.json` sidecar |
| **depot** | a bundle store addressed as `type:location` (`r2:aha-depot`, `local:~/agent-depot`); `type ∈ {local, r2}`; the configured one is your default, `--depot` overrides | renames the v1 "aggregation point"/`bundle_out_dir`; one noun for the store, its address, and the default role; implies central storage without implying source control |
| **depot driver** | the code implementing a depot type (`local`, `r2`) | implementation detail in `internal/depot`; not a user-facing entity |
| **catalog** | the bundle listing in a depot, sharded per machine | distinct from the corpus's full-text index |
| **corpus** | the local SQLite + FTS database built from bundles | unchanged from v1; "index" only ever refers to its FTS component |
| **`BundleRef`** | an in-code reference to a bundle (key + metadata) | a reference type, not a second name for a bundle |

Retired from earlier drafts so no entity carries two names:

- **`receipt`** as a name for a bundle (it is the `.receipt.json` sidecar) → use
  **bundle**.
- **`destination`** as a separate noun → collapsed into **depot** (the depot *is*
  the addressable store; `type:location` is just its address form).
- **`index`** as a key prefix → **catalog** (frees "index" for the corpus's FTS).
- the **`push`/`pull`/`sync`** verbs → generalized `snapshot`/`ingest`/`refresh`
  (`sync` collided with the locked "Live sync: No" non-goal).

## Architecture and flows

Per-machine pipeline — only `snapshot`/`ingest` cross the depot boundary;
everything analytical stays on the local corpus:

```text
  LOCAL SOURCES                 DEPOT (local dir or r2:aha-depot)        LOCAL CORPUS
  ~/.pi  ~/.claude  ~/.codex          [ shared bundle pool ]            [ per-machine database ]
        │ discover+parse                                                       
        ▼                                                                      
   aha snapshot ──── bundle ──────push──►  bundles/v1/<sha>.tar.zst             
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

  One bundle pool, three independent corpora. Convergence is pull-driven and eventual:
  a machine only knows the bundles it has ingested. Nobody ever queries the depot to search.
```

## Opinionated defaults

**R2 depot bucket: `aha-depot`.** No suffix, no machine name, no random token.
Lowercase, 9 chars, a valid R2/S3 bucket name, one per account.

This must be opinionated for **correctness, not tidiness**: a depot only
aggregates if every machine lands in the **same** bucket. A per-machine or
randomised default would silently create separate buckets that never merge — the
opposite of the feature. So the default must be stable and identical everywhere;
a second depot is an explicit override (`--depot r2:aha-archive`).

Local depot default `~/.aha/depot`, overridable with `--depot local:/some/path`
(a USB drive, NFS mount, or synced folder — which is how a *local* depot can
still be shared across machines). A local depot is just a directory whose
internal layout **mirrors the R2 key space**, so the `local` and `r2` drivers
stay symmetric (one writes paths, the other object keys):

```text
<depot-root>/                       # default ~/.aha/depot ; or --depot local:PATH
  depot.json                        # marker: schema, depot_id, layout version
  bundles/
    v1/
      <bundle_sha256>.tar.zst       # the immutable bundle, named by content hash
      ...
  catalog/
    v1/
      <machine_id>.json             # one append-mostly shard per machine
```

The bundle file is named by its **content hash**, not the pretty
`aha-sessions-…` name; the human-readable filename and per-bundle provenance live
in the catalog shard (on R2 they may also sit in object metadata; a local FS has
none, so the catalog is the canonical record).

Populated, for the three-machine walkthrough:

```text
~/.aha/depot/
  depot.json
  bundles/v1/
    3f9a…A.tar.zst          # from ade-mbp
    7c21…B.tar.zst          # from work-mac
    a8e0…C.tar.zst          # from linux-box
    b4d5…A2.tar.zst         # ade-mbp, later/grown
  catalog/v1/
    ade-mbp.json            # lists 3f9a…A, b4d5…A2
    work-mac.json           # lists 7c21…B
    linux-box.json          # lists a8e0…C
```

A **catalog shard** (`catalog/v1/<machine>.json`) is append-mostly, one per
machine so there is no write contention:

```json
{
  "schema": "aha-depot-catalog/v1",
  "machine_id": "ade-mbp",
  "bundles": [
    { "bundle_sha256": "3f9a…", "bundle_id": "01J…",
      "captured_at": "2026-05-23T09:14:02Z", "bytes": 734512, "sessions": 41,
      "filename": "aha-sessions-ade-mbp-2026-05-23T09-14-02Z-3f9a.tar.zst" }
  ]
}
```

The **`depot.json`** marker lets `depot init` create or recognize a depot and
`depot verify` check the layout version (the restic repo-config / kopia
format-blob analog):

```json
{ "schema": "aha-depot/v1", "depot_id": "…", "layout": "v1",
  "created_at": "2026-05-23T09:00:00Z", "created_by": "aha 0.1.0" }
```

**Atomic writes.** `snapshot`/push writes to a temp file and renames into
`bundles/v1/<sha>.tar.zst`, skipping if the hash already exists — the same
temp-file + atomic-rename + skip-if-present discipline the corpus blob store
already uses — so a transient `bundles/v1/.tmp-*` may appear briefly during a
write.

**The depot is not the corpus, even when both are local.** They are separate
subtrees under `~/.aha`:

```text
~/.aha/
  depot/        ← the depot (shared source of truth: bundles + catalog)
  corpus.db     ← the corpus (local database/index)
  blobs/        ← corpus's own content-addressed blobs (files/, images/, …)
```

Honest cost: with a local depot the same bundle bytes can exist twice — once in
`depot/bundles/v1/<sha>.tar.zst` and again in the corpus blob store after
`ingest`. That duplication is the price of a fully self-contained corpus so
`read`/`search` work offline even when the depot is a remote R2 bucket. Possible
later optimization: reflink/hardlink when depot and corpus share a filesystem, or
let a local depot double as the corpus bundle store — deferred, not v1.

The `v1/` segment versions the key layout. The key scheme, the `aha-depot/v1`
marker, the `aha-depot-catalog/v1` shard schema, the manifest schema, and the
bundle content hash are **explicit, versioned public contracts** (see Hyrum's
Law), not incidental internals.

## Depot vs. corpus

Two subsystems with different jobs, lifetimes, and locations:

| | **Depot** | **Corpus** |
|---|---|---|
| Holds | immutable, content-addressed bundles | SQLite + FTS database built from bundles |
| Scope | **one, shared** across machines | **one per machine**, local |
| Role | durable **source of truth** | **derived**, disposable, rebuildable |
| Written by | `snapshot` | `ingest` |
| Read by | `ingest` | `search` / `read` / `status` / `conflicts` |
| Needs network | yes (if R2) | no — always local |

Mental model in brand-neutral terms: the depot is a **shared folder of immutable
bundles**; the corpus is a **locally-built search index** over them — like macOS
Spotlight, Windows Search, or `locate`'s database on Linux: derived from your
files, rebuildable from scratch, and never the source of truth. Nobody "searches
the shared folder" — you search your local corpus, which you keep fed from the
folder. That is why there is **no shared remote corpus**: querying R2 per search
would mean many round-trips, lose offline use, and force a single-writer SQLite
file in a bucket — all rejected for conceptual integrity (see audit). **Depot =
the bundle aggregation point, full stop. The corpus stays local.**

## Commands in the depot model

Organizing rule: **only `snapshot` (write), `ingest` (read), `refresh` (both),
and the `depot`/`doctor` diagnostics touch the depot. Everything analytical reads
the local corpus only and works offline.** The depot is implicit (you chose it
once); any depot-touching command can override it for one invocation with
`--depot <depot>` (e.g. `--depot r2:aha-archive`).

| Command | Depot | Corpus | Network | Behavior |
|---|---|---|---|---|
| `aha init` | — | — | no | scaffolds config; records your depot choice |
| `aha depot init <depot>` | create/bind | — | yes (r2) | creates or connects the depot, writes it to config |
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
  what is in your local corpus; the gap is what the next `refresh` pulls.
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

- **One bundle pool, many corpora.** After T4 the depot holds `{A, B, C, A2}` —
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

- `internal/depot` package: a **depot driver** interface with `local` and `r2`
  implementations, plus a minimal S3 **SigV4** signer. **Recommendation:** a
  small hand-rolled SigV4 client over `net/http` + stdlib
  `crypto/sha256`/`crypto/hmac` to preserve `aha`'s pure-Go, CGO-free,
  minimal-dependency ethos; alternative `minio-go`. Either way the signer needs
  golden SigV4 vector tests.
- `depot` config block (`type` + `location`, off by default), `aha depot
  init/ls/verify`, and `--depot` on the depot-touching commands.
- Per-machine catalog shard reader/writer; content-addressed `bundles/v1/<sha>`
  keys.
- **Optional** client-side encryption (off by default — see Security). `aha`
  deliberately diverges from restic/kopia here: a mandatory key is itself a
  data-loss vector, and the guiding principle is data loss > data theft.
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
  opt-in, private, scoped, integrity-checked," and add a depot-security
  guarantee.
- README privacy warning: uploading **unredacted** bundles is real exposure even
  to a private bucket; recommend v2 redaction and/or the optional client-side
  encryption before enabling a remote depot.
- `writeSnapshot`/`cmdIngest`/`cmdRefresh` route bundle bytes through the
  selected depot driver.
- Relocate the default bundle store from `~/agent-session-bundles`
  (`bundle_out_dir`) to the local depot `~/.aha/depot`. No migration shim — there
  are no users (see Backwards compatibility).

### Remove / explicitly avoid

- Do **not** weaken the local-first default; the depot is opt-in.
- Do **not** store `corpus.db` in R2 as a shared writable query engine (unsafe
  multi-writer SQLite; contradicts "SQLite is the engine, not a cache").
- Avoid querying bundles in place over the network for analyses.
- Retire `snapshot`/`refresh` `--out`; the bundle destination is named only via
  `--depot` (e.g. `--depot local:./bundles`). Safe to drop outright — no users.
- Retire the words `destination`, `receipt` (as a name for a bundle), `index`
  (as a key prefix), and the `push`/`pull`/`sync` verbs.

## Trade-offs

| Dimension | Local-only v1 (today) | With a depot (proposed) |
|---|---|---|
| Trust model | "Everything stays on your machine." Mechanically proven no-network. | Histories leave the machine; no-network becomes "local-only by default." Biggest cost. |
| Secrets | Bundles never uploaded; v1 does not redact. | Unredacted content lands in third-party storage → mitigated by a private bucket + TLS + scoped tokens + R2 at-rest encryption, with redaction (v2) or optional client-side encryption as add-ons. |
| Setup | Zero credentials. | Cloudflare account, bucket, scoped tokens. |
| Dependencies | Pure-Go, tiny dep set. | Adds an S3 client / SigV4 (dep weight or hand-rolled + tested). |
| Multi-machine | Manual bundle copy + `ingest`. | One shared depot; `refresh` converges automatically. |
| Durability | One disk. | Cloudflare-managed redundancy; corpus rebuildable from the depot. |
| Offline | Always. | Analyses still local/offline; only snapshot-push and ingest-pull need network. |
| Failure surface | Filesystem only. | + auth, throttling, partial uploads, partitions. |

Net: trades `aha`'s strongest privacy property for durable, low-friction,
egress-free multi-machine aggregation — acceptable only as an explicit,
off-by-default opt-in, with confidentiality handled by defenses that cannot
orphan data.

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

The bucket is **private and treated as holding unredacted secrets** — but, per
the guiding principle, the security posture prioritizes defenses that protect
*availability* and never risk orphaning data.

1. **Private bucket only** — no public bucket, no `r2.dev` URL, no public custom
   domain.
2. **Scoped, least-privilege API tokens — never account-scoped.** Per-machine
   write tokens for snapshot; read-only tokens for pull-only/analysis hosts; one
   token per machine so a lost laptop is revoked without rotating everyone.
3. **Credentials never in repo or bundles.** Loaded from env, OS keychain, or a
   `0600` file outside the corpus/depot trees. `aha` must assert credentials
   never appear in `manifest.json`, bundle bytes, `--json`, the `.receipt.json`
   sidecar, or config.
4. **TLS-only transport**, SigV4 signing.
5. **Integrity verification on download** — already enforced: ingest re-hashes
   every file against the manifest and the bundle SHA against the key; tampered
   or truncated objects are rejected before promotion.
6. **Anti-deletion / retention (data-loss priority).** Enable object versioning
   or bucket lock so bundles cannot be silently overwritten or deleted;
   immutability + content addressing makes any mutation detectable. This is the
   security control that matters most given data loss > data theft.
7. **Optional client-side encryption (off by default).** Users who need
   zero-knowledge storage can opt in (e.g. `age`/AES-256-GCM with a user-held
   key), accepting the explicit trade: **if the key is lost, the bundles are
   unrecoverable** — a data-loss risk the default avoids. R2 already encrypts at
   rest, so the default relies on that plus the private bucket and TLS.
8. **Auditability** — R2 event notifications / access logs.

## Prior art

We are reinventing a well-trodden shape: content-addressed immutable snapshots to
an object-store backend with a separate, rebuildable index.

| Tool | What we borrow | Difference / lesson |
|---|---|---|
| **restic** | `repository`/`snapshot`/`index`/`pack`/`backend` vocabulary; S3 backend; default-repo vs `--repo`; `check`/`prune` lifecycle | encrypts client-side **by default** — we deliberately **diverge** (optional only), because a mandatory key is a data-loss vector; its "index" is blob-location, not full-text, so our corpus is richer |
| **kopia** | object-store-native, **multi-client-safe** shared repository; clean backend/index/manifest layering | closest operational analog to "many machines, one depot"; our content-addressed unique keys give the same write-safety |
| **borg** | dedup ideas | **not** object-store-native (needs FS/SSH, in-place mutation) — the example of what *not* to require |
| **perkeep** | content-addressed blobs + a **separate, rebuildable search index** + multi-backend sync | closest *architectural* analog to the bundle↔corpus split; validates keeping search local and the depot a pure blob store |
| **DVC / git-annex / OCI** | `push`/`pull` of content-addressed blobs to a `remote` | precedent for the verbs; we still prefer generalizing `snapshot`/`ingest` over minting them |
| **rclone** | object-store transfer | its `sync` is a destructive mirror — a cautionary tale for the word "sync" |
| `geekmuse/chronicle` | multi-machine session sync, path canonicalization | syncs **live files** (Git backend, CRDT); we replicate **immutable bundles** on demand |
| `badlogic/pi-share-hf` | incremental collection, **redaction, review, upload** | the exact snapshot→redact→upload shape, plus the redaction we flag as a prerequisite |

Key transferable lessons: **the index/corpus is derived from blobs** (perkeep) —
keep search local and the depot a pure bundle pool; and where restic/kopia
encrypt by default, we consciously choose **not** to, because for an archive a
lost key is worse than a read bucket.

## Testing and verification

This work is verified to two standards at once: it must **prove the new depot
functionality** and **prove v1 still works untouched**. The approach follows the
installed `testing-best-practices` skill — real objects over mocks, table-driven
cases, golden files for contracts, property/fuzz tests, regression-test-first,
contract tests for the external API, both-directions for security, and **no live
network in the default suite**.

### Harness and layout

- `internal/depot/*_test.go` holds the driver **contract suite**; `t.TempDir()`
  for `local` depots and corpora.
- The `r2` driver is tested against an **in-process S3 fake** (`gofakes3`, or
  MinIO) — real requests, real SigV4, **no live network**. A real R2 bucket is
  exercised only behind `//go:build integration` with env credentials.
- Reuse the existing realistic bundle/corpus fixtures and the idempotent
  `IngestBundle` path; do not mock the filesystem, tar/zstd, SQLite, or S3.
- Time is injected (for backoff); no `sleep`, no wall-clock, no live network in
  the default `go test ./...` run.

### Test-type matrix

| Type | Coverage for the depot feature |
|---|---|
| Smoke | `aha depot --help`, `aha depot init/ls/verify`, and a `snapshot --depot local:…` → `ingest --depot local:…` → `search` round trip all run. |
| Contract / differential | One suite asserts **identical** observable behavior for the `local` driver, the S3 fake, and (tagged) real R2. |
| Unit | depot address parsing (`type:location`), catalog shard read/write/merge, content-hash key derivation, sync-delta computation. |
| Golden | `bundles/v1/` key layout, `aha-depot/v1` marker, `aha-depot-catalog/v1` shard, depot `--json` output; the manifest and bundle bytes stay **byte-identical** to v1. |
| Property / fuzz | `Get(Put(x)) == x`; push is idempotent; `pull set == catalog − corpus`; the address parser never panics on arbitrary input. |
| Integrity / regression | a tampered or truncated object is rejected on SHA mismatch and never promoted (written test-first, red→green). |
| Security (both directions) | credentials authenticate **and** never appear in any manifest, bundle, `.receipt.json`, `--json`, or config; the depot is private by default. |
| Concurrency / race | parallel pushes of unique content-addressed keys; per-machine catalog shards show no write contention; passes `go test -race`. |
| Throttling | the S3 fake returns HTTP 429 → bounded exponential backoff with an injected clock. |
| Encryption (opt-in path) | `decrypt(encrypt(x)) == x`; ciphertext ≠ plaintext; wrong key fails closed. |
| Doc-sync | new commands/flags/config keys match the registry and config struct (extends `docs_test.go`, `flag_metadata_sync_test.go`). |
| No-regression | the depot-off path is byte-identical to v1; the reworked no-network test; the full v1 suite green. |

### Core properties and invariants

- **Driver symmetry:** the `local` and `r2` drivers pass one shared contract
  suite — identical behavior, only transport differs.
- **Roundtrip / idempotency / sync-delta / integrity** as above, asserted on
  *exact* sets and hashes, never "non-empty."
- **Depot-off inertness:** with no depot configured,
  `snapshot`/`ingest`/`refresh`/`search`/`read`/`status`/`conflicts` take the
  local path and produce output identical to v1 (golden + characterization).
- **No-network core (test-first):** the reworked static test fails the build if
  any package other than `internal/depot` imports a network package; add it
  **before** writing the `r2` driver.
- **Trust Guarantees 1 & 2 unchanged:** snapshot read-only behavior and
  immutable-bundle ingest are unaffected; their existing tests pass unchanged.
- **Determinism preserved:** transport never alters bundle bytes, the manifest,
  or the content hash — the determinism tests guard this as a now-public contract.
- **Both-directions security:** credentials work **and** never leak.

### Test-quality rules (from the skill)

- Prefer real temp dirs, real archives, real SQLite, and a real (fake) S3 server;
  avoid hand-written mocks of those boundaries.
- No weak sole assertions ("not empty", truthy); assert exact key sets, hashes,
  JSON, and negative cases.
- Every bug gets a failing regression test first; record the red phase or state
  why it could not be observed.
- No unconditional skips — real-R2 tests live behind a build tag with a
  documented rationale.
- Golden-file changes require human review because they define the public
  contracts (key layout, catalog/manifest schemas, JSON).
- For security and transform tests, assert **both** directions
  (rejected/removed *and* preserved/usable).

### Validation commands

```bash
go test ./...                                   # full suite, depot off by default
go test -race ./...                             # concurrency/race
go vet ./...
go test ./internal/depot/...                    # driver contract suite vs local + S3 fake
go test -tags integration ./internal/depot/...  # real R2 (env credentials)
go test ./internal/cli -run 'NoNetwork|Docs|Readme'   # trust + doc-sync
go build ./cmd/aha
```

### Manual / end-to-end

Run the three-machine flow (two machines `snapshot --depot`, one `ingest
--depot`) against the S3 fake or a scratch R2 bucket; confirm `search` returns
hits from both machines with `--machine` filtering, then delete the corpus and
rebuild it from the depot to prove the corpus is disposable.

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
  nouns (depot, catalog) but **no new verbs**: `snapshot` still makes a bundle,
  `ingest` still builds the corpus, now location-aware via `--depot`. The tool
  reads as "the same `aha`, with the depot able to live in R2."
- **One name per entity.** Earlier drafts carried dual names; this revision fixed
  them: **bundle** (never "receipt", which is the `.receipt.json` sidecar),
  **depot** (the single noun for the store, its address, and the default role —
  the separate "destination" noun was collapsed in), **corpus** (with "index"
  reserved for its FTS component), and **catalog** (not "index") for the depot
  listing. This reverses my earlier defense of a depot-vs-destination
  role/primitive split in favor of the single-name principle.
- **Clean layering** — depot driver (transport) / depot (logical store) / corpus
  (database) — mirrors restic (backend/repo) and perkeep (blobs/index).
  Principled, not ad hoc.
- **Defaults preserved.** Depot off by default; all v1 behavior, output, and
  trust guarantees unchanged — the relaxation is explicit and bounded.

**Genuine tensions (bounded and decided, not hidden):**

1. **"Live sync: No" non-goal vs. `refresh` doing push+pull.** `refresh`
   replicates **discrete immutable bundles on demand** — `git fetch`-like, not
   Dropbox-like. It does not continuously sync or mutate live session files, so
   it honors the non-goal's spirit; the word "sync" is kept off the command
   surface.
2. **"Everything stays on your machine" vs. the whole feature.** The biggest
   shift, resolved by opt-in + off-by-default + a confidentiality posture that
   never risks data. Integrity holds *because* the default is unchanged and the
   relaxation is explicit.

**Resolved by the guiding principle (no longer a core tension):**

- **Content-addressing vs. encryption.** Earlier this was the sharpest tension
  because encryption was default: zero-knowledge encryption fights dedup-by-
  plaintext-SHA. With encryption now **optional and off by default**, the default
  path has no conflict — dedup and content addressing are clean. A user who opts
  into encryption accepts the documented trade (weaker cross-machine dedup and a
  key-loss risk); the tool does not impose it.

**Minor warts (acceptable, noted not hidden):**

- `--depot` is meaningful for depot-touching commands but inert for
  `search`/`read`/`conflicts`; `status --depot` is a normally-local command that
  optionally reaches the network. Acceptable with clear help text.
- Two `init` verbs (`init`, `depot init`) — mirrors git `init` vs `remote add`.

**Overall:** high conceptual integrity. The feature extends the existing pipeline
rather than paralleling it, every entity now has a single name, and the remaining
tensions are bounded, deliberate relaxations of v1 posture rather than internal
contradictions.

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
  counts and discards the deterministic, content-addressed bundle.
- **Client-side encryption by default** — rejected: a mandatory key is a
  data-loss vector, and data loss outweighs data theft for an archive.
- **`push`/`pull`/`sync` verbs** — rejected: `sync` collides with a non-goal; the
  others carry git baggage. Generalizing `snapshot`/`ingest` via `--depot` keeps
  the bundle→corpus seam visible.

## Definition of done for this feature

- Depot off by default; all v1 behavior, output, golden files, and trust
  guarantees unchanged.
- `internal/depot` is the only package importing a network package, enforced by
  the reworked static test.
- `snapshot`/`ingest`/`refresh` + `aha depot init/ls/verify` work through the
  depot-driver contract suite against the `local` driver and an in-process S3
  fake; real-R2 tests exist behind a build tag.
- Integrity verification rejects tampered/truncated objects (regression test).
- Anti-deletion (object versioning / bucket lock) documented as the primary
  confidentiality-independent durability control.
- Client-side encryption is **optional and off by default**; if enabled,
  roundtrip + wrong-key tests pass and the key-loss data-loss risk is documented.
- Credentials never appear in any emitted artifact (security test, both
  directions).
- `docs/trust.md` and README state the opt-in depot posture and the
  unredacted-upload warning; doc-sync covers new commands/flags/config.
- Key layout, catalog schema, manifest schema, and content-hash determinism are
  versioned and golden-tested as public contracts.
- Every entity has exactly one name across spec, code, and docs.
