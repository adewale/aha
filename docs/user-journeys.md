# User journeys

The 0.2 journey model is:

```text
Agent histories --archive upload--> Archive --archive download--> Workspace
```

## First local use

```bash
aha init --acknowledge-raw-history
aha archive upload
aha archive download
aha search 'query'
aha show '<ref>'
```

Observable transitions:

```text
reachable uninitialised Archive → empty Archive → populated Archive
absent Workspace → current Workspace
```

## Upload without a Workspace

```bash
aha archive init 'local:/Volumes/private/aha-archive'
aha archive upload 'local:/Volumes/private/aha-archive'
```

Upload reads configured agent histories and mutates only the Archive. It does not create, open, lock, or update a Workspace.

## Download without local agent histories

```bash
aha archive download 'r2:team-history' --workspace "$HOME/aha-team"
```

Download reads the Archive and mutates only the Workspace. It does not inspect local agent-history roots and never writes the Archive.

## Aggregate machines

Machine A:

```bash
aha archive upload 'r2:team-history'
```

Machine B:

```bash
aha archive upload 'r2:team-history'
```

Processing machine:

```bash
aha archive download 'r2:team-history' --workspace "$HOME/aha-all"
aha workspace status "$HOME/aha-all"
```

The Workspace contains each indexed machine's latest complete snapshot. Older manifests remain durable in the Archive but are excluded from materialisation.

## Independent upload and download gaps

```bash
aha status --json
```

Possible normal system states:

| State | Meaning | Next transition |
|---|---|---|
| `current` | local histories are uploaded and Workspace matches Archive latest | none |
| `upload_needed` | this machine's histories differ from its latest Archive snapshot | `aha archive upload` |
| `download_needed` | Workspace vector differs from Archive latest | `aha archive download` |
| `upload_and_download_needed` | both independent gaps exist | `aha archive upload` first |

There is no combined `sync` operation and therefore no false atomicity across the two transitions.

## Repeated no-op operations

```bash
aha archive upload --json
aha archive upload --json
aha archive download --json
aha archive download --json
```

An unchanged upload reports `reused:true`. A current download reports `no_op:true` and does not open the Workspace for writing.

## Archive mismatch

```bash
aha archive download 'r2:archive-b' --workspace "$HOME/workspace-bound-to-a"
```

The command rejects `archive_mismatch` during side-effect-free preflight. It does not create a lock, WAL, database, config change, or Archive write.

## Repair local processing state

```bash
aha workspace verify "$HOME/aha-work"
aha workspace repair "$HOME/aha-work" --backup
```

Repair materialises from the bound Archive, verifies and syncs the replacement, atomically exchanges directories, and retains the old Workspace at the reported backup path.

## Inspect evidence

```bash
ref="$(aha search 'migration failure' --refs | head -1)"
aha show "$ref" --md
```

Search results are leads. `show` retrieves the source context needed to evaluate them.

## Analyse recurring failures

```bash
aha analyse failures --state unresolved
aha analyse failures --state resolved --project myrepo
```

The analysis groups normalised command families and error signatures, then ranks observed resolution paths. It does not create or install skills.

## Use a non-default Workspace

```bash
aha workspace set-default "$HOME/aha-project"
aha archive download --workspace "$HOME/aha-project"
aha search --workspace "$HOME/aha-project" 'query'
```

Selecting a Workspace changes local config only. It does not create or modify the Workspace.

## Select an Archive

```bash
aha archive status 'r2:team-history'
aha archive set-default 'r2:team-history'
```

Selection requires an initialised, healthy Archive and changes local config only.

## Automation

```bash
aha archive upload --progress=json --json >upload.json 2>upload-progress.ndjson
aha archive download --progress=json --json >download.json 2>download-progress.ndjson
```

Successful stdout is one JSON document. Progress and the terminal error envelope use stderr.
