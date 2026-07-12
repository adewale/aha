# Onboarding

## Local-first setup

```bash
aha version --json
aha init --acknowledge-raw-history
aha archive status
aha archive upload
aha archive download
aha status --json
```

The privacy acknowledgement is mandatory because raw agent histories may contain source code, prompts, shell output, credentials, and images.

Default resources:

```text
Archive:   local:~/.aha/archive
Workspace: ~/.aha/workspace
Config:    ~/.config/aha/config.jsonc
```

`init` does not create the Workspace database. The first `archive download` materialises it.

## Validate the result

```bash
aha workspace verify
aha search 'needle' --refs
aha show '<returned-ref>' --md
aha analyse failures
aha mcp check
```

`aha status` is the normal diagnostic command. It reports one of `current`, `upload_needed`, `download_needed`, or `upload_and_download_needed`, plus one next transition.

## Use an existing shared R2 Archive

Create the R2 bucket externally and issue a bucket-scoped Object Read & Write token. Ordinary object credentials are not treated as bucket-administration authority.

```bash
export AHA_R2_ACCOUNT_ID='your-account-id'
export AHA_R2_ACCESS_KEY_ID='your-access-key-id'
export AHA_R2_SECRET_ACCESS_KEY='your-secret-access-key'

aha archive status 'r2:team-history'
aha archive upload 'r2:team-history'
aha archive download 'r2:team-history' --workspace "$HOME/aha-team"
aha status --archive 'r2:team-history' --workspace "$HOME/aha-team"
```

If status reports `uninitialised` for a new bucket:

```bash
aha archive init 'r2:team-history'
```

Initialisation writes only the Archive marker. It does not upload histories and does not select the Archive as the default.

To select it:

```bash
aha archive set-default 'r2:team-history'
```

Only non-secret R2 account/endpoint settings are written to config. Access keys remain in the environment.

## Aggregate several machines

On each machine:

```bash
aha archive upload 'r2:team-history'
```

On the machine that needs the aggregated Workspace:

```bash
aha archive download 'r2:team-history' --workspace "$HOME/aha-all-history"
```

Download selects every machine's latest complete snapshot. Historical manifests remain Archive-only.

## Repair a Workspace

```bash
aha workspace verify "$HOME/aha-work"
aha workspace repair "$HOME/aha-work" --backup
```

Repair is backup-preserving and supported only where aha can provide atomic directory exchange. It never rebinds a Workspace to another Archive.

## Common blocked states

| State | Meaning | Transition |
|---|---|---|
| `invalid_address` | Explicit address lacks `r2:`/`local:` or is malformed | correct the address and rerun `archive status` |
| `invalid_configuration` | R2 field is missing, conflicting, or a placeholder | correct the named safe field |
| `unreachable` | Archive cannot be inspected | restore access, then rerun `archive status` |
| `uninitialised` | Reachable location has no Archive marker | `aha archive init ARCHIVE` |
| v1 Archive layout | Pre-0.2 storage is preserved but cannot be migrated in place | initialise a fresh Archive such as `local:~/.aha/archive-v2` |
| `damaged` Archive | Marker/index/pointer/manifest/blob invariant failed | `aha archive verify ARCHIVE --deep` |
| `absent` Workspace | No Workspace database exists | `aha archive download` |
| `behind` Workspace | Archive latest vector has advanced | `aha archive download` |
| `archive_mismatch` | Workspace belongs to another Archive | use the matching Archive/Workspace pair |
| `invalid_destination` | Path is unrelated, non-empty, or overlaps a source | choose a dedicated destination |
| `damaged` Workspace | SQLite/FTS/local invariant failed | `aha workspace repair --backup` |

## Progress and automation

```bash
aha archive upload --progress=plain
aha archive download --progress=json --json \
  >result.json 2>progress.ndjson
```

Progress never appears on stdout and never includes a fabricated ETA. Failed JSON commands append one `aha.error.v1` object to stderr.

## Dashboard and MCP

```bash
aha dashboard
aha mcp check
aha mcp serve
```

The dashboard binds to loopback by default. Remote binding requires explicit acknowledgement and a bearer token. MCP is read-only; `check` opens the Workspace and registers the complete tool surface without serving stdio.
