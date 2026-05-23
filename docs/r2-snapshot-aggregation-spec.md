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
  - Remote bundle aggregation
  - aha remote
---

# R2 Snapshot Aggregation Spec

## Status and relationship to v1

This is a **proposed** design, not a locked v1 decision. It extends the v1
system described in `docs/agent-history-aggregator-spec.md` by adding an
**optional, opt-in** remote aggregation tier backed by a **private Cloudflare R2
bucket**. It deliberately crosses two v1 boundaries — "local-only, no network"
(Guarantee 3 in `docs/trust.md`) and "do not upload bundles" (the README
privacy warning) — so it is a v2-class capability and must remain off by
default.

The v1 design already contains the seam that makes this tractable:

> The bundle is the receipt. The corpus is the index.

Bundles are immutable, deterministic, and content-addressed by SHA-256. That is
exactly the contract object storage wants. This spec routes those same bundle
bytes through R2 instead of (or in addition to) the local
`bundle_out_dir`, and keeps every analysis running against a local SQLite + FTS
corpus that is hydrated from the aggregated bundle set.

## Goal

Let many machines publish their snapshot bundles into one **shared private R2
bucket**, and let any authorized machine reconstruct or update a local corpus
from the union of those bundles. R2 becomes the shared, durable,
egress-free **distribution layer for bundles**; the local corpus stays the
**query engine** for the analyses `aha` already supports (`search`, `read`,
`status`, `conflicts`, `doctor`).

```txt
machine A ─ snapshot ─┐
machine B ─ snapshot ─┼─► private R2 bucket (immutable tar.zst bundles + index)
machine C ─ snapshot ─┘                 │
                                        ▼
                       any machine ─ pull/sync ─► local corpus.db + FTS ─► search/read/...
```

## Cloudflare R2 facts this design relies on

From `https://developers.cloudflare.com/r2/how-r2-works/` and the R2 limits
documentation, the properties that matter here:

- **S3-compatible API**, SigV4 auth, region `auto`; also a Workers binding and a
  REST API. The S3 API is the data plane we target.
- **No egress fees.** Pulling the full bundle set to many machines is not
  metered by bytes downloaded; operations (Class A writes/lists, Class B reads)
  are metered.
- **Strong consistency** (read-after-write, list-after-write), which makes
  "push then another machine pulls" reliable without convergence hacks.
- **Encrypted at rest by default**; per-bucket and scoped API tokens.
- Relevant limits: max object **5 TiB**; single-part PUT **5 GiB**; multipart up
  to **10,000 parts / 4.995 TiB**; key length **1,024 bytes**; **unlimited**
  objects/bytes per bucket; up to **1,000,000 buckets/account**; S3/Workers API
  recommended over the REST API for high object counts; account-wide REST API
  cap ~**1,200 req / 5 min**; same-object concurrent writes throttled to ~1/s
  (HTTP 429).

Two consequences for `aha`:

1. The current `MaxBundleBytes = 2 GiB` cap (see `internal/archive/archive.go`)
   fits comfortably under the **5 GiB single-PUT** limit, so multipart is **not
   required** for v1 of this feature. If the cap is ever raised past 5 GiB,
   multipart upload becomes mandatory.
2. Bundles already aggregate many tiny session files into **one** object. That
   keeps object counts and per-object operation costs low — a much better fit
   for R2 economics than uploading raw session files individually.

## Proposed refactoring

### The seam: a `BundleStore` abstraction

Today the bundle location is hardcoded to the local filesystem. `snapshot`
writes to `cfg.BundleOutDir` (`internal/cli/cli.go:writeSnapshot`), and `ingest`
globs/reads local paths (`internal/cli/command_ingest.go`). Introduce one
interface that both local and remote backends implement:

```go
// internal/remote/bundlestore.go  (network code lives ONLY in this package)
type BundleStore interface {
    // Put stores bundle bytes under an opaque, content-addressed key.
    // Implementations must be idempotent: putting identical bytes twice is a no-op.
    Put(ctx context.Context, obj BundleObject, r io.Reader) error
    // Get returns the bundle bytes for a key.
    Get(ctx context.Context, key string) (io.ReadCloser, error)
    // List returns known bundle objects, optionally filtered by prefix.
    List(ctx context.Context, prefix string) ([]BundleObject, error)
    // Stat reports whether a key exists and its recorded metadata.
    Stat(ctx context.Context, key string) (BundleObject, bool, error)
}

type BundleObject struct {
    Key        string // e.g. "bundles/v1/<bundle_sha256>.tar.zst"
    BundleSHA  string
    BundleID   string
    MachineID  string
    CapturedAt string
    Bytes      int64
}
```

- `LocalFSStore` wraps the existing `bundle_out_dir` behavior. It is the default
  and contains **no network code**.
- `R2Store` wraps the S3-compatible API. It is the **only** package allowed to
  import `net/http`.

`snapshot`/`refresh` write through `BundleStore.Put`; `ingest`/`pull`/`sync`
read through `Get`/`List`. The corpus, archive, adapters, search, and read code
do not change at all — they keep operating on local `tar.zst` files and the
local `corpus.db`. The remote backend only changes **how bundle bytes travel**.

### Object key layout (a versioned, documented contract)

Primary objects are **content-addressed** so identical bundles from different
machines de-duplicate for free and re-uploads are no-ops:

```txt
bundles/v1/<bundle_sha256>.tar.zst        # the immutable bundle bytes
index/v1/<machine_id>.json                # per-machine append-only index shard
```

- Content-addressed keys (not the human bundle filename) are the storage
  identity. The pretty filename
  `aha-sessions-{machine}-{ts}-{id}.tar.zst` is kept only as object metadata for
  human browsing.
- **Per-machine index shards** avoid expensive full-bucket `LIST` on every sync.
  Each machine only ever writes its own shard (no write contention), and a
  reader merges shards. The bucket-wide `LIST` remains the fallback / repair
  path.
- The `v1/` segment versions the layout. The key scheme and index schema are
  **explicit public contracts** (see Hyrum's Law, below), not incidental
  internals.

### New commands / flags

Add a thin remote surface; reuse all existing ingest/corpus machinery:

| Command | Behavior |
|---|---|
| `aha push [bundle...]` | Upload local bundles to the configured R2 bucket; skip objects already present (by SHA); update this machine's index shard. |
| `aha pull [--since ...]` | Download bundles present in R2 but not yet ingested locally, then ingest them (reusing idempotent `corpus.IngestBundle`). |
| `aha sync` | `push` then `pull`: publish local snapshots and absorb everyone else's. |
| `aha remote ls` / `aha remote verify` | List/inspect remote bundles; verify object SHAs and index/bucket agreement. |

Plus a `--remote` flag on `refresh` so the existing one-command journey can
optionally publish-and-aggregate. With remote disabled (default), every command
behaves exactly as today.

### Sync algorithm (incremental, idempotent)

```txt
local set  = { bundle_sha256 in corpus.bundles }            # already tracked in SQLite
remote set = union(index shards) ∪ (fallback LIST bundles/v1/)
to_pull    = remote set − local set
for each missing bundle: Get → stage → validate manifest+file SHAs → IngestBundle
```

Correctness falls out of properties already in the codebase:

- `corpus.IngestBundle` is **idempotent** (`recordBundleAttempt` dedupes by
  `bundle_id` and `bundle_sha256`), so re-pulling a bundle is a no-op.
- Ingest already **stages, validates, and only then promotes** bundle bytes
  (`bundlePlanner.Prepare` → `walkTarZstd` SHA checks → `PromoteBundle`), so a
  truncated or tampered download is rejected before it can corrupt the corpus.
- Append-only merge and conflict quarantine already handle the same session
  showing up from multiple machines.

### Config additions

Extend `model.Config` (new fields, all optional, default off):

```jsonc
{
  // ...existing v1 config...
  "remote": {
    "enabled": false,
    "provider": "r2",
    "account_id": "<cf-account-id>",
    "bucket": "aha-private-corpus",
    "endpoint": "https://<account>.r2.cloudflarestorage.com",
    "region": "auto",
    "prefix": "bundles/v1/",
    // Credentials are NEVER stored here. They come from env or an OS keychain.
    "credentials_source": "env",        // "env" | "keychain" | "file:~/.config/aha/r2-credentials"
    "encryption": "none"                // "none" | "age" | "aes-gcm" (client-side, see Security)
  }
}
```

## What must change, be added, or be removed

### Add

- `internal/remote` package: `BundleStore` interface, `LocalFSStore`, `R2Store`,
  and a minimal S3 SigV4 signer. **Recommendation:** a small hand-rolled SigV4
  client over `net/http` + stdlib `crypto/sha256`/`crypto/hmac`, to preserve
  `aha`'s pure-Go, minimal-dependency, CGO-free ethos. Alternative:
  `minio-go` (pure Go, lighter than `aws-sdk-go-v2`). The trade-off is
  hand-rolled-and-tested vs. dependency-weight; either way the signer needs
  golden SigV4 vector tests.
- `aha push` / `pull` / `sync` / `remote` commands wired into the registry
  (`internal/cli/cli.go`), with `--json`, docs metadata, and doc-sync coverage.
- `remote` config block in `model.Config` + `config.Default()` (off by default).
- Credential loading from env / keychain / 0600 file — never from committed
  config, never written into manifests or bundles.
- Per-machine index shard reader/writer and a content-addressed key scheme.
- Optional **client-side encryption** of bundle bytes before upload.

### Change

- **The no-network guarantee must be re-scoped, not deleted.**
  `internal/cli/security_static_test.go` currently fails the build if **any**
  non-test file under `cmd/` or `internal/` imports `net`, `net/http`,
  `net/url`, or `net/rpc`. The test comment already anticipates this:
  *"update docs/trust.md if v1 network behavior changes."* The change is to
  **exempt only `internal/remote`** and assert that **every other package stays
  net-free**, so the local-only guarantee is still mechanically enforced for the
  core. (Alternative: gate `internal/remote` behind a `//go:build r2` tag and
  keep default builds 100% net-free.)
- `docs/trust.md`: reframe Guarantee 3 from "no network, ever" to "**local-only
  by default; remote is opt-in, private, scoped, integrity-checked**," and add a
  new guarantee describing remote security posture.
- README privacy warning: uploading **unredacted** bundles to R2 is a real
  exposure even in a private bucket. Strengthen the warning and recommend
  enabling v2 redaction and/or client-side encryption before turning remote on.
- `writeSnapshot` / `cmdIngest` / `cmdRefresh` route bundle bytes through a
  `BundleStore` selected by config.

### Remove / explicitly avoid

- Do **not** remove or weaken the local-first default. Remote stays opt-in.
- Do **not** store the live `corpus.db` in R2 as a shared writable query engine.
  SQLite is not safe for concurrent multi-writer access over object storage, and
  it conflicts with "SQLite is the engine, not a cache." A read-only published
  corpus snapshot is a possible **future** convenience (below), not the model.
- Avoid querying bundles in place over the network for analyses — see rejected
  alternatives.

## Trade-offs

| Dimension | Local-only v1 (today) | With R2 aggregation (proposed) |
|---|---|---|
| Trust model | "Everything stays on your machine." Mechanically proven no-network. | Histories leave the machine. No-network becomes "local-only by default." Biggest cost. |
| Secrets exposure | Bundles never uploaded; v1 does not redact. | Unredacted prompts/code/credentials land in a third party's storage. Demands redaction or client-side encryption. |
| Setup cost | Zero credentials, zero accounts. | Cloudflare account, bucket, scoped tokens, key management. |
| Dependencies | Pure-Go, CGO-free, tiny dep set. | Adds an S3 client / SigV4 (dep weight or hand-rolled-and-tested). |
| Multi-machine merge | Manual: copy a bundle, `aha ingest`. | One shared bucket; `aha sync` converges automatically. |
| Durability | One laptop's disk. | Cloudflare-managed redundancy. |
| Offline analysis | Always works. | Snapshot/search/read still local; only push/pull need network. |
| Failure surface | Filesystem only. | + auth failures, throttling, partial uploads, network partitions. |

Net: the design trades the strongest privacy property `aha` has for durable,
low-friction, egress-free multi-machine aggregation. That trade is only
acceptable as an explicit, off-by-default opt-in paired with redaction or
encryption.

## Performance and scalability concerns

- **Operation cost, not byte cost, dominates.** No egress fees, but each push is
  a Class A `PUT`, each `LIST` is Class A, each pull is a Class B `GET`. The win
  comes from bundles aggregating many session files into one object — keep that
  property; never fan out to per-session objects.
- **Avoid full-bucket `LIST` on every sync.** S3 `LIST` paginates at 1,000 keys;
  a corpus of N bundles costs ⌈N/1000⌉ Class A ops and grows unbounded. The
  per-machine index shards make sync cost proportional to **machines**, not
  total bundles. Full `LIST` stays as a repair path only.
- **Pull is N round trips.** Ingesting K new bundles is K serial `GET`s plus the
  existing CPU-bound parse + FTS work. Parallelize downloads (bounded
  concurrency) and stream into the existing staging path; the SQLite write is the
  serialization point (`db.SetMaxOpenConns(1)`), so overlap network with parse.
- **Bundle size vs. PUT limit.** 2 GiB cap < 5 GiB single PUT today. Raising the
  cap past 5 GiB forces multipart (≤10,000 parts).
- **Same-object write throttle (~1/s).** Irrelevant because content-addressed
  keys are unique; re-PUT of an existing object is skipped via `Stat`.
- **Query performance does not improve.** R2 aggregates *bundles*; analyses still
  run on local SQLite. A very large aggregate corpus is an FTS/SQLite scaling
  problem independent of R2 (indexes, `status` counts, conflict queries). R2
  neither helps nor hurts query latency.
- **Strong consistency simplifies sync** (read-after-write), so no
  convergence/backoff dance is needed for "push then pull."
- **Throttling.** Use the S3/Workers data-plane API (not the REST API's
  ~1,200 req/5min cap), honor HTTP 429 with bounded exponential backoff, and cap
  concurrent requests.

## Security: how access to the bucket is secured

The bucket is **private and treated as holding unredacted secrets.** Layers:

1. **Private bucket only.** No public bucket, no `r2.dev` dev URL, no public
   custom domain. The bucket is never internet-readable.
2. **Scoped, least-privilege API tokens — never account-scoped.**
   - Per-machine **write+read** or **write-only** tokens for snapshot/push.
   - **Read-only** tokens for pull-only consumers / analysis hosts.
   - One token per machine so a lost laptop is revoked without rotating
     everyone.
3. **Credentials never live in the repo or in bundles.** Loaded from env
   (`AWS_ACCESS_KEY_ID`/`AWS_SECRET_ACCESS_KEY` or R2 equivalents), an OS
   keychain, or a `0600` credentials file outside the corpus/bundle trees.
   `aha` must assert credentials never appear in `manifest.json`, bundle bytes,
   `--json` output, receipts, or committed config.
4. **TLS-only transport** to the S3 endpoint; SigV4 request signing.
5. **Integrity verification on download.** Already enforced: ingest re-hashes
   every file against the manifest and the bundle SHA against the key. A tampered
   or truncated object is rejected before promotion. This defends against a
   compromised or buggy storage layer.
6. **Client-side encryption (recommended given no redaction).** Optionally
   encrypt bundle bytes (e.g. `age` or AES-256-GCM with envelope keys) **before**
   upload so Cloudflare only ever sees ciphertext. The encryption key is held by
   the user, never in the bucket. Trade-off: the stored object's hash is the
   ciphertext hash, so the plaintext `bundle_sha256` must travel in object
   metadata / the index, and dedup is over ciphertext (deterministic encryption
   or a content-derived key preserves dedup; random nonces break it — pick per
   threat model).
7. **Tamper resistance / retention.** Enable object versioning or bucket lock so
   bundles cannot be silently overwritten or deleted; immutability + content
   addressing already makes any mutation detectable.
8. **Auditability.** Enable R2 event notifications / access logs so pushes and
   reads are observable.

## Verification: how we verify the new functionality

Per the installed `testing-best-practices` skill (real objects over mocks,
table-driven, golden files, property tests, regression-test-first, contract
tests for external APIs, both-directions for security, no live network in unit
tests):

- **One `BundleStore` contract suite, run against every implementation.** The
  same table-driven behavior tests execute against `LocalFSStore`, an in-process
  S3 fake (`gofakes3` / in-memory), and — gated behind a `//go:build
  integration` tag with env credentials — a real R2 bucket. This is
  differential testing: all backends must satisfy identical observable behavior.
- **Use a real S3-compatible server in tests, not hand-written mocks.**
  `gofakes3` in-process (or MinIO) gives real request/response and SigV4
  behavior with **no live network**, satisfying "prefer real behavior" while
  keeping CI hermetic. Avoid mocking the S3 client.
- **SigV4 golden vectors.** Sign known requests and assert against AWS SigV4
  published test vectors — a contract test that catches signer drift.
- **Roundtrip property:** `Get(Put(x)) == x` and the returned bytes hash to the
  expected SHA, over arbitrary bundle contents.
- **Idempotency:** pushing the same bundle twice yields one object; pulling +
  ingesting twice adds zero logical rows (reuse existing ingest-idempotence
  tests through the remote path).
- **Integrity regression (test-first):** mutate one byte of a stored object →
  assert pull/ingest rejects it on SHA mismatch and never promotes it.
- **Sync delta correctness:** given a known local ingested set and a known remote
  index, assert the *exact* set of keys pulled equals `remote − local` (not just
  "non-empty").
- **Multi-machine end-to-end:** machine A and machine B push distinct bundles
  through the fake store; machine C `pull`s and `search` returns hits from both
  machines and can filter by `--machine`. This promotes the existing
  "second machine" canonical example to run through R2.
- **Encryption (if enabled):** `decrypt(encrypt(x)) == x`; ciphertext ≠
  plaintext; wrong key fails closed; both directions asserted.
- **Security assertions (both directions):** credentials are usable for auth
  **and** never appear in any emitted artifact (manifest, bundle, receipt,
  `--json`, logs).
- **Throttling/backoff:** fake store returns HTTP 429 → client retries with
  bounded backoff and ultimately succeeds or returns a structured error
  (deterministic, injected clock — no real sleeps).
- **Doc-sync:** new commands/flags/config keys are validated against the registry
  and config struct by the existing doc-sync test pattern
  (`internal/cli/docs_test.go`, `flag_metadata_sync_test.go`).
- **No unconditional skips:** real-R2 tests live behind a build tag with a
  documented rationale, not silent `t.Skip`.

## Verification: how we confirm we did not break existing functionality

- **Whole existing suite stays green:** `go test ./...`, `go test -race ./...`,
  `go vet ./...`, parser fuzz, determinism tests, and **all golden files
  unchanged** when remote is disabled. Bundle bytes and manifests are byte-for-
  byte identical because R2 only changes transport.
- **The no-network guarantee stays mechanically enforced for the core.** Rework
  `TestNoNetworkImportsInApplicationPackages` so it still fails the build if any
  package **other than `internal/remote`** imports a network package. This is the
  key regression guard for the trust model; add it test-first before writing
  `R2Store`.
- **Remote-off path is provably inert:** with `remote.enabled=false`,
  `snapshot`/`ingest`/`refresh`/`search`/`read`/`status`/`conflicts` take the
  local code path and produce identical output (golden + characterization tests).
- **Trust Guarantees 1 and 2 untouched:** snapshot read-only behavior and
  immutable-bundle ingest are unaffected (R2 changes neither source reads nor
  parse-time identity). Their existing tests must still pass unchanged.
- **Characterization on the bundle format:** existing v1 bundles round-trip
  through `Put`/`Get` and ingest identically — the tar.zst/manifest schema is not
  modified by this feature.
- **Performance non-regression:** large-corpus ingest/parse benchmarks are
  unchanged with remote off; remote on adds only network time around the
  unchanged local pipeline.

## Hyrum's Law implications

> With a sufficient number of users of an API, it does not matter what you
> promise in the contract: all observable behaviors of your system will be
> depended on by somebody.

Putting bundles in a shared bucket vastly widens the set of *observable*
behaviors, so things `aha` currently treats as internal become de facto
contracts:

- **Object key layout.** Once anyone writes a lifecycle rule, dashboard, or
  `aws s3 ls` script against `bundles/<machine>/<date>/...`, that layout is load-
  bearing even though it is "internal." Mitigation: the explicit `bundles/v1/`
  and `index/v1/` versioned prefixes, golden-tested key/index shapes, and a
  documented migration path before any layout change.
- **Bundle filename and manifest schema.** Because bundles are now directly
  fetchable, external tools will parse `manifest.json` and the tar layout
  themselves — fields marked `omitempty` or "internal" will be depended on.
  Treat the bundle schema (`agent-session-snapshot-bundle/v1`) as a real public
  contract with explicit versioning, not an implementation detail.
- **The content hash is now a contract.** Determinism + content addressing means
  consumers will pin and dedup on `bundle_sha256`. Any change to zstd level, tar
  metadata normalization, or manifest field ordering changes the hash and breaks
  dedup and external pins. The existing determinism tests must guard this even
  harder once hashes are shared.
- **Index schema.** `index/v1/<machine>.json` will be read by people, not just
  `aha`. Version it and golden-test it.
- **Strong consistency.** R2's read-after-write will be relied upon for "push
  then immediately pull." Document this as the assumed model so a future caching
  or multi-region change is evaluated against that dependency.
- **JSON output and error/throttle behavior.** New `--json` remote fields, and
  even observable retry timing / HTTP 429 surfacing, will be scripted against.
  Once present they are effectively unremovable; add them deliberately and keep
  them stable.

General mitigations, consistent with `aha`'s existing discipline: prefer
**explicit versioned contracts** over "this is internal" labels (labels do not
stop dependence), pin and golden-test every externally observable surface (keys,
index, manifest, hashes, JSON), and ship a deprecation/migration path rather than
silent breaking changes.

## Rejected and deferred alternatives

- **Store `corpus.db` in R2 as the shared query engine.** Rejected: SQLite is
  unsafe for concurrent multi-writer access over object storage, and it
  contradicts "SQLite is the engine, not a cache."
- **Query bundles in place over the network for each analysis.** Rejected:
  every `search`/`read` would become many network round trips and lose offline
  use, FTS, and determinism.
- **Publish a read-only prebuilt `corpus.db` snapshot to R2** for fast bootstrap
  of new machines. Deferred as a future convenience: a single-writer host
  publishes an immutable, versioned, read-only corpus snapshot that others
  download to seed before incremental `pull`. Treated as a cache/replica, never
  the multi-writer source of truth.
- **Per-session objects instead of bundles.** Rejected: explodes object/op
  counts and discards the deterministic, content-addressed bundle that makes
  dedup and integrity trivial.

## Definition of done for this feature

- `remote.enabled=false` by default; all v1 behavior, output, golden files, and
  trust guarantees are unchanged.
- `internal/remote` is the only package importing a network package, enforced by
  the reworked static test.
- `push`/`pull`/`sync` work through a `BundleStore` with a contract suite passing
  against local FS and an in-process S3 fake; real-R2 tests exist behind a build
  tag.
- Integrity verification rejects tampered/truncated objects (regression test).
- Credentials never appear in any emitted artifact (security test, both
  directions).
- `docs/trust.md` and the README state the new opt-in remote posture and the
  unredacted-upload warning; doc-sync tests cover new commands/flags/config.
- Key layout, index schema, manifest schema, and content-hash determinism are
  versioned and golden-tested as public contracts.
