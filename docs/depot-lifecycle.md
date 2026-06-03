# Depot lifecycle

A **depot** is the durable store for your agent-history *bundles* — immutable
`tar.zst` snapshots — plus a small catalog that records what each machine has
pushed. It sits between your local agent histories and your local search corpus:

```text
local agent histories → snapshot bundle → depot → local corpus → search/read
```

This document captures a depot's lifecycle: the states it moves through, the
commands that move it, and how the default depot is chosen and switched. For the
on-disk/object layout and per-command data flows see
[`architecture.md`](architecture.md); for first-time R2 setup see
[`onboarding.md`](onboarding.md) §8 and
[`r2-bucket-settings.md`](r2-bucket-settings.md); for full command metadata see
[`commands.md`](commands.md).

## The model in two dimensions

Keeping these two **independent** dimensions separate is the whole model:

- **Provisioning lifecycle of a depot** — its backing store moves through
  **Uninitialized → Initialized → Populated**. `aha` provisions it for you on the
  first write, so you rarely run `aha depot init` by hand.
- **Selection** — exactly one depot is the **default** at a time, recorded as a
  config pointer (`depot.type` / `depot.location`). Out of the box it points at a
  local depot (`~/.aha/depot`) that is not yet provisioned; nothing touches the
  network until you use a remote.

A depot can be fully Initialized and Populated *without* being the default.
*Configuring* a depot moves the pointer — `aha depot init <addr>` (provision and
select) or `aha depot use <addr>` (switch to an already-initialized depot). A
`--depot <addr>` flag is a one-off override: it targets a depot for a single
command without moving the pointer (and may still auto-initialize that target).

## State machine

![Depot lifecycle: provisioning lifecycle × selection](depot-lifecycle.svg)

Text version of the diagram:

```text
Provisioning lifecycle of a single depot
----------------------------------------
                aha depot init                  snapshot / refresh
  Uninitialized ───────────────▶ Initialized ────────────────▶ Populated
   (no depot.json)                (depot.json marker)            (bundles + catalog)
        │                          ↺ init (idempotent)           ↺ snapshot · ls · verify · compact
        │  first snapshot / refresh  →  auto-init + populate
        └────────────────────────────────────────────────────────▶ Populated

  Populated ⇄ Degraded   — marker missing / catalog drift; heal with `verify --repair`
  any state → (rm dir / delete bucket — external, not aha) → Uninitialized

Selection — which initialized depot is the default (a config pointer)
--------------------------------------------------------------------
  config default ──(aha depot init <addr> | aha depot use <addr>)──▶ { local depot, r2 depot, … }
  --depot <addr>  = one-off override; never moves the pointer (may still auto-init <addr>)
  Only an Initialized depot may be selected; aha depot use refuses an uninitialized target.
```

### Provisioning states

1. **Uninitialized** — the backing store has no `depot.json` marker **and no
   bundle/catalog evidence**: a brand-new local default, or a bucket created in
   the dashboard but never provisioned. `aha doctor` reports it reachable with
   `initialized: false` (`ok: true`) and an `aha depot init ...` next-action
   hint, not as an error.
2. **Initialized** — the `depot.json` marker is present (`schema: aha-depot/v1`,
   `layout: v1`). Reached by `aha depot init` **or implicitly by the first
   `aha snapshot` / `aha refresh`**, which auto-create the dir/bucket and marker
   before writing.
3. **Populated** — at least one bundle under `bundles/v1/<sha>.tar.zst` is
   recorded in a per-machine catalog shard `catalog/v1/<machine>.json`.
4. **Degraded** (a sub-state of Initialized/Populated) — populated depot evidence
   exists but the marker is missing, or the catalog and bundle objects have
   drifted. `aha depot verify` flags it and `aha depot verify --repair` heals it
   by rebuilding the catalog/marker from the bundle objects.
5. **Decommissioned** — the backing store was removed **outside `aha`** (`rm` the
   directory, or delete the bucket and revoke its token). A default *local* depot
   then reads as Uninitialized again.

### Selection (orthogonal to the states above)

Selection is **not** a provisioning state — it is the config pointer naming the
default depot. `aha depot init` and `aha depot use` move it (only ever to an
Initialized depot); `--depot <addr>` overrides it for a single command. Switching
the default leaves every depot's data untouched.

## Transitions (commands)

| Command | Provisioning | Selection | Net (r2) | Effect |
|---|---|---|---|---|
| `aha depot init <addr>` | Uninitialized → Initialized (idempotent) | sets default = `<addr>` | yes | Creates the dir/bucket if needed, writes the `depot.json` marker, and for r2 persists the non-secret `depot.r2.account_id`. Re-running against an existing depot just connects. |
| `aha depot use <addr>` | requires Initialized | sets default = `<addr>` | yes | Switches the default to an already-initialized `<addr>`; refuses a reachable-but-uninitialized target and points at `aha depot init`. Persists r2 `account_id`. Creates nothing. |
| `aha snapshot` | Uninitialized → Initialized (auto) → Populated | unchanged | yes | Auto-initializes the target if needed, then builds a bundle and pushes it (`bundles/v1/<sha>.tar.zst` + catalog), atomic write, skip-if-present. Does not touch the corpus. |
| `aha refresh` | same as `snapshot` → Populated | unchanged | yes | `snapshot` (auto-init + push, or reuse unchanged state), then `ingest` pending depot bundles into the local corpus. |
| `aha ingest` | reads Populated (no provisioning) | unchanged | yes | Reads bundles new to this machine from the depot and merges them into the local corpus. |
| `aha depot ls` | reads Initialized/Populated | unchanged | yes | Lists the catalog refs across all machines' shards. |
| `aha depot verify [--deep] [--repair]` | Degraded → Initialized/Populated (with `--repair`) | unchanged | yes | Quick: marker + catalog↔object metadata. `--deep`: re-hashes/downloads bundle bytes. `--repair`: rebuilds catalog/marker from the bundle objects. |
| `aha depot compact` | Populated (in place) | unchanged | yes | Deduplicates repairable catalog refs by bundle SHA without downloading bundle bytes. |

`--depot <addr>` on `snapshot` / `refresh` / `ingest` / `status` / `doctor` runs
that one command against `<addr>` without moving the default pointer. Because the
write commands auto-initialize their target, a one-off `snapshot --depot
r2:other` can create/initialize `r2:other` as a side effect.

## What lives in a depot

```text
<depot>/
  depot.json                       # marker: schema, depot_id, layout, created_*
  bundles/v1/<sha>.tar.zst         # immutable history bundles, keyed by content hash
  catalog/v1/<safe-machine-id>.json # per-machine catalog shard (append-only)
```

- **`depot.json`** lets `init` recognize an existing depot and lets `verify`
  check the layout version. It is the restic repo-config / kopia format-blob
  analog.
- **Bundles** are immutable and content-addressed; a re-push of identical bytes
  is skipped.
- **Catalog shards** are per-machine and merged on read, so two machines never
  contend on the same object. The catalog is an acceleration/provenance layer:
  `aha depot verify --repair` can rebuild it entirely from `bundles/v1/*`.

## Walkthrough

### Local (the default)

```bash
aha init --accept-secrets   # writes config with the local depot default
aha refresh                 # auto-initializes ~/.aha/depot, then snapshots + ingests
aha doctor                  # depot: local:~/.aha/depot ok=true, initialized=true
```

No `aha depot init` is needed for the local default — the first `refresh`
provisions it.

### Promote to R2

Configuring R2 makes it the default. Only the two **secret** keys ever live in
the environment; `init` persists the non-secret account id to config:

```bash
export AHA_R2_ACCESS_KEY_ID=... AHA_R2_SECRET_ACCESS_KEY=... AHA_R2_ACCOUNT_ID=...
aha depot init r2:aha-depot   # create/connect, write marker, set as default
aha refresh                   # now targets R2, no --depot flag
```

### Add another machine

The bucket already exists and is initialized, so the second machine just selects
it (after exporting the two secret keys, plus the account id the first time):

```bash
aha depot use r2:aha-depot
aha refresh                   # shares history through R2
```

### Switch the default back

```bash
aha depot use local:~/.aha/depot   # back to local — both depots keep their data
aha depot use r2:aha-depot         # back to R2
```

## Maintenance

```bash
aha depot ls --json                 # what's in the shared pool
aha depot verify --json             # quick: marker + catalog/object metadata
aha depot verify --deep --json      # re-hash/download bundle bytes (slow, explicit)
aha depot verify --repair --json    # rebuild catalog/marker from bundle objects
aha depot compact --json            # dedupe catalog refs without downloading bytes
```

## Decommissioning

Depots are not deleted by `aha`. To stop using one, switch the default away
(`aha depot use <other>`) and then remove the backing store yourself: delete the
local directory, or delete the R2 bucket and revoke its API token. Bundles are
immutable and content-addressed, so a depot can be safely abandoned without
affecting any corpus already ingested from it.

## Where the configuration lives

- `depot.type` / `depot.location` — the default depot (e.g. `r2` / `aha-depot`).
- `depot.r2.account_id` — non-secret, persisted by `init` / `use` so a configured
  R2 default works in a fresh shell. An explicit `endpoint` (jurisdiction or
  fake-S3) and a non-`auto` `region` are persisted only when set.
- **Never in config:** `AHA_R2_ACCESS_KEY_ID` and `AHA_R2_SECRET_ACCESS_KEY`.
  Keep these two secrets in the environment or a secret manager (a direnv
  `.envrc` is a convenient home).
