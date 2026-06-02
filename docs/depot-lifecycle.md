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

## The model in one paragraph

There is exactly **one default depot** at a time, recorded in config
(`depot.type` / `depot.location`). Out of the box it is **local** —
`~/.aha/depot` — and nothing touches the network. *Configuring* a depot
overrides the default, and two commands configure it: `aha depot init` (create
and select) and `aha depot use` (switch to an already-initialized one). A
`--depot <addr>` flag on any command is a one-off override and never changes the
default.

## States

```text
  unconfigured ──init──▶ initialized ──snapshot/refresh──▶ populated
  (local default,        (depot.json                        (bundles +
   no depot.json)         marker written)                    catalog shards)

        ▲                       │                                  │
        └───── depot use ───────┴──── depot use / ls / verify ─────┘
               (switch the default depot, inspect, maintain — in place)
```

1. **Unconfigured** — the default `local:~/.aha/depot`, before anything is
   written. There is no `depot.json` marker yet. `aha doctor` reports this depot
   as reachable but `initialized: false` (`ok: true`) with a `next` hint to run
   `aha depot init`, rather than as an error.
2. **Initialized** — `aha depot init <addr>` has created the backing store
   (a directory for `local`, a bucket for `r2`) and written the `depot.json`
   marker (`schema: aha-depot/v1`, `layout: v1`). The depot is now also the
   configured default.
3. **Default / selected** — the depot the config points at; every flagless
   command (`snapshot`, `refresh`, `ingest`, `status`, `doctor`, `depot …`)
   targets it. `aha depot use <addr>` moves this selection between initialized
   depots.
4. **Populated** — `aha snapshot` / `aha refresh` have stored one or more
   bundles under `bundles/v1/<sha>.tar.zst` and recorded them in the
   per-machine catalog shard `catalog/v1/<machine>.json`.
5. **Maintained** — kept healthy in place with `aha depot verify` /
   `verify --repair` / `aha depot compact`; inspected with `aha depot ls`.

## Transitions (commands)

| Command | From → To | Network (r2) | Effect |
|---|---|---|---|
| `aha depot init <addr>` | unconfigured/uninitialized → **initialized + default** | yes | Creates the dir/bucket if needed, writes the `depot.json` marker, sets `<addr>` as the default depot, and for r2 persists the non-secret `depot.r2.account_id`. Idempotent: re-running against an existing depot connects to it. |
| `aha depot use <addr>` | initialized → **default** (switch) | yes | Switches the default depot to an already-initialized `<addr>`. Refuses a reachable-but-uninitialized target and points you at `aha depot init`; for r2 it persists the non-secret `depot.r2.account_id`. Creates nothing. |
| `aha snapshot` | initialized → **populated** | yes | Builds a bundle from local sources and pushes it (`bundles/v1/<sha>.tar.zst` + catalog), atomic write, skip-if-present. Does not touch the corpus. |
| `aha refresh` | initialized → **populated** | yes | `snapshot` into the depot (or reuse unchanged state), then `ingest` pending depot bundles into the local corpus. |
| `aha ingest` | populated → (corpus) | yes | Reads bundles new to this machine from the depot and merges them into the local corpus. |
| `aha depot ls` | populated (inspect) | yes | Lists the catalog refs across all machines' shards. |
| `aha depot verify [--deep] [--repair]` | any (check/heal) | yes | Quick: marker + catalog↔object metadata. `--deep`: re-hashes/downloads bundle bytes. `--repair`: rebuilds catalog/marker from the bundle objects. |
| `aha depot compact` | populated (optimize) | yes | Deduplicates repairable catalog refs by bundle SHA without downloading bundle bytes. |

`--depot <addr>` on `snapshot` / `refresh` / `ingest` / `status` / `doctor`
runs that single command against `<addr>` without changing the default.

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
aha refresh                 # creates ~/.aha/depot on first use, snapshots + ingests
aha doctor                  # depot: local:~/.aha/depot ok=true, initialized=true
```

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
