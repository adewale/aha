# Command inventory

Aha 0.2 has one lifecycle:

```text
Agent histories --archive upload--> Archive --archive download--> Workspace
```

There are no compatibility aliases for the pre-launch command surface. See [`docs/commands.md`](commands.md) for generated flag and JSON metadata.

## Initialisation

```bash
aha init --acknowledge-raw-history
```

Writes config, establishes a stable machine identity, initialises the default local Archive, and prepares (but does not create) the default Workspace destination.

## Archive

```bash
aha archive init [ARCHIVE]
aha archive set-default ARCHIVE
aha archive status [ARCHIVE]
aha archive upload [ARCHIVE]
aha archive download [ARCHIVE] --workspace PATH
aha archive verify [ARCHIVE] [--deep]
```

`archive upload` reads agent histories and mutates only the Archive. `archive download` reads the Archive and mutates only the Workspace. Neither operation silently performs the other.

## Workspace

```bash
aha workspace set-default PATH
aha workspace status [PATH]
aha workspace verify [PATH] [--repair-fts]
aha workspace repair [PATH] --backup
aha workspace conflicts [PATH]
```

A Workspace is bound to one Archive identity. Repair materialises a verified replacement, atomically exchanges it, and preserves the prior Workspace as a backup.

## Inspection and processing

```bash
aha status [--archive ARCHIVE] [--workspace PATH]
aha search [--workspace PATH] QUERY
aha show [--workspace PATH] REF
aha analyse failures [--workspace PATH]
aha dashboard [--workspace PATH]
aha mcp check [--workspace PATH]
aha mcp serve [--workspace PATH]
```

`status` reports the independent upload and download gaps and emits at most one next action.

## Deliberately absent

The 0.2 surface has no `sync`, portable import/export, standalone snapshot listing, public SQLite optimisation, or unsafe no-backup repair operation. The former `snapshot`, `refresh`, `ingest`, `depot`, `corpus`, `doctor`, `read`, `incidents`, `conflicts`, `serve`, and top-level `verify` commands are removed rather than aliased.
