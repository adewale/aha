# aha commands

This file is generated from CLI command metadata. Update command metadata, then regenerate this file.

## Global profiling

Any command may write Go pprof profiles with `--cpuprofile FILE` and/or `--memprofile FILE`. These flags can appear before or after the subcommand, or be supplied via `AHA_CPU_PROFILE` and `AHA_MEM_PROFILE`. Profiles are local debugging artifacts and are not written unless explicitly requested.

Examples: `aha --cpuprofile cpu.pprof search needle`, `aha verify --memprofile heap.pprof`.

## Error contract

Every failed command prints one concise, credential-safe error and exactly one `next:` action. Raw dependency, SQL, SDK, and filesystem errors are not public output. Add global `--verbose-errors` for allowlisted diagnostics (failure kind, operation, retryability), never raw causes.

When a command is invoked with `--json`, failures are written to stderr using the stable `aha.error.v1` envelope:

```json
{
  "schema": "aha.error.v1",
  "error": {
    "code": "machine_readable_code",
    "message": "safe human-readable message",
    "command": "command-name",
    "next": ["aha doctor --json"],
    "next_action": {"command": "aha", "args": ["doctor", "--json"]},
    "diagnostics": []
  }
}
```

With `--progress=json`, stderr remains valid NDJSON: progress events are followed by one terminal `aha.error.v1` object on failure.

## aha conflicts

list quarantined merge conflicts

```txt
aha conflicts [--repo DIR] [--json]
```

**Flags:**

- `--config`
- `--corpus`
- `--json`
- `--repo`

**Examples:**

- `aha conflicts --json`

**JSON contract:** `array<object{id,session_key,entry_id,first,second,created_at}>`

## aha corpus

inspect corpus disk usage, vacuum SQLite, explicitly prune unreferenced blobs, or atomically rebuild a pre-v2 corpus while preserving a backup

```txt
aha corpus <size|vacuum|prune-orphans|rebuild> [--repo DIR] [--progress MODE] [--json] [--force|--backup]
```

**Flags:**

- `--backup`
- `--config`
- `--corpus`
- `--force`
- `--json`
- `--progress`
- `--repo`

**Examples:**

- `aha corpus size --json`
- `aha corpus vacuum`
- `aha corpus prune-orphans --json`
- `aha corpus rebuild --backup --json`

**JSON contract:** `object{root,total_bytes,database_bytes,file_blob_bytes,image_blob_bytes,other_bytes,files}|object{before_bytes,after_bytes,reclaimed_bytes}|object{root,dry_run,orphan_bytes,deleted_files,deleted_bytes,orphans}|object{root,backup,next,next_action}`

## aha depot

preflight R2 with one safe next action, initialize a depot, switch the default, list snapshots, or verify content

```txt
aha depot <setup|init|use|ls|verify> [DEPOT] [--progress MODE] [--json] [--deep]
```

**Flags:**

- `--config`
- `--deep`
- `--json`
- `--progress`

**Examples:**

- `aha depot setup r2:aha-depot --json`
- `aha depot init local:~/.aha/depot`
- `aha depot init r2:aha-depot`
- `aha depot use r2:aha-depot`
- `aha depot ls --json`
- `aha depot verify --deep`

**JSON contract:** `object|array`

## aha doctor

show diagnostics and exactly one state-aware next action

```txt
aha doctor [--depot DEPOT] [--json]
```

**Flags:**

- `--config`
- `--depot`
- `--json`

**Examples:**

- `aha doctor`
- `aha doctor --depot local:~/.aha/depot --json`

**JSON contract:** `object{version,config,adapters,sources,corpus,depot,next,next_action}`

## aha export

materialize a machine's latest depot snapshot as a portable v1 bundle.tar.zst (the single-file hand-off format; re-import with aha ingest)

```txt
aha export [--machine ID] [--depot DEPOT] [--out FILE] [--json]
```

**Flags:**

- `--config`
- `--depot`
- `--json`
- `--machine`
- `--out`

**Examples:**

- `aha export`
- `aha export --machine work-mac --out work.tar.zst`

**JSON contract:** `object{bundle,sha256,manifest_sha256,machine,files,bytes}`

## aha incidents

rank recurring tool-call failures with their resolution status (unresolved/partial/resolved) and the fix paths that worked

```txt
aha incidents [--repo DIR] [--limit N] [--state S] [--project P] [--source S] [--machine M] [--tool T] [--json]
```

**Flags:**

- `--config`
- `--corpus`
- `--json`
- `--limit`
- `--machine`
- `--project`
- `--repo`
- `--source`
- `--state`
- `--tool`

**Examples:**

- `aha incidents --json`
- `aha incidents --state unresolved`
- `aha incidents --state resolved --project myrepo`

**JSON contract:** `array<object{tool_name,command_family,error_signature,episodes,distinct_sessions,distinct_projects,resolved,resolution_rate,state,tier,first_seen,last_seen,spark,paths:array<object{families,support,distinct_sessions,distinct_projects,confidence,sample_ref,sample_ordinal}>,sample_ref,score}>`

## aha ingest

pull every machine's latest depot snapshot into the corpus (fetching only unknown content), or import explicit v1 bundle files

```txt
aha ingest [--repo DIR] [--depot DEPOT] [--progress MODE] [--json] [bundle.tar.zst ...]
```

**Flags:**

- `--config`
- `--corpus`
- `--depot`
- `--progress`
- `--repo`
- `--json`

**Examples:**

- `aha ingest ./bundle.tar.zst`
- `aha ingest --repo ./aha-repo`
- `aha ingest --depot local:~/.aha/depot`

**JSON contract:** `array<object{machine?,manifest_sha256?,bundle?,sessions,entries,messages,images,artifacts,duplicate}>`

## aha init

write starter JSONC config

```txt
aha init [--config PATH] [--accept-secrets] [--json]
```

**Flags:**

- `--accept-secrets`
- `--config`
- `--json`

**Examples:**

- `aha init --accept-secrets`

**JSON contract:** `object{config,accepted_secrets}`

## aha mcp

run a read-only stdio MCP server over the corpus

```txt
aha mcp [--config PATH] [--repo DIR] [--dry-run]
```

**Flags:**

- `--config`
- `--corpus`
- `--dry-run`
- `--repo`

**Examples:**

- `aha mcp`
- `aha mcp --dry-run`

**JSON contract:** `jsonrpc:tools/list|tools/call (stdio MCP)`

## aha read

retrieve source context for a search result

```txt
aha read [REF] [--session ID] [--entry ID] [--repo DIR] [--before N] [--after N] [--json|--md]
```

**Flags:**

- `--after`
- `--before`
- `--branch`
- `--config`
- `--corpus`
- `--entry`
- `--json`
- `--live`
- `--md`
- `--repo`
- `--session`

**Examples:**

- `aha read <ref_text> --json`
- `aha read --session <session> --entry <entry> --json`

**JSON contract:** `array<object{line_no,entry_id,timestamp,role,text,raw_json}>`

## aha refresh

push this machine's state to the depot (unchanged state is recognized without re-uploading), then pull every machine's latest snapshot into the corpus

```txt
aha refresh [--session MATCH ...] [--max-sessions N] [--repo DIR] [--depot DEPOT] [--force] [--progress MODE] [--json]
```

**Flags:**

- `--accept-secrets`
- `--captured-at`
- `--config`
- `--corpus`
- `--depot`
- `--force`
- `--machine`
- `--max-sessions`
- `--progress`
- `--repo`
- `--session`
- `--source`
- `--json`

**Examples:**

- `aha refresh`
- `aha refresh --session abc --max-sessions 1`

**JSON contract:** `object{push:object{manifest_sha256,reused,files,blobs_uploaded,blobs_carried},report,reports}`

## aha search

find relevant messages/artifacts; use read on returned refs before answering

```txt
aha search <query> [--repo DIR] [--source NAME] [--machine ID] [--role ROLE] [--project KEY] [--path-token TOKEN] [--json|--refs|--files|--md]
```

**Flags:**

- `--after`
- `--before`
- `--config`
- `--corpus`
- `--files`
- `--json`
- `--limit`
- `--machine`
- `--md`
- `--path`
- `--path-token`
- `--project`
- `--refs`
- `--repo`
- `--role`
- `--source`

**Examples:**

- `aha search needle --json`
- `aha search needle --refs`

**JSON contract:** `array<object{score,timestamp,source,machine,project,role,snippet,session_key,entry_id,ref,ref_text}>`

## aha serve

run a read-only local dashboard over the corpus on loopback

```txt
aha serve [--addr HOST:PORT] [--allow-remote] [--allowed-hosts H1,H2] [--timeout DUR] [--token TOKEN] [--config PATH] [--repo DIR]
```

**Flags:**

- `--addr`
- `--allow-remote`
- `--allowed-hosts`
- `--config`
- `--corpus`
- `--repo`
- `--timeout`
- `--token`

**Examples:**

- `aha serve`
- `aha serve --addr 127.0.0.1:18428`
- `aha serve --allow-remote --token $(openssl rand -hex 32)`

**JSON contract:** `http://HOST:PORT/api/{search,read,incidents,incident_trajectory,overview,status,verify,conflicts,corpus_size,doctor}`

## aha snapshot

push this machine's state to the depot: upload only new file versions, publish a snapshot manifest, move the pointer (no corpus needed; never downloads other machines' data)

```txt
aha snapshot [--session MATCH ...] [--max-sessions N] [--depot DEPOT] [--force] [--progress MODE] [--json]
```

**Flags:**

- `--accept-secrets`
- `--captured-at`
- `--config`
- `--depot`
- `--force`
- `--machine`
- `--max-sessions`
- `--progress`
- `--session`
- `--source`
- `--json`

**Examples:**

- `aha snapshot --accept-secrets --depot local:~/.aha/depot`

**JSON contract:** `object{manifest_sha256,reused,files,blobs_uploaded,blobs_carried}`

## aha status

summarize corpus health

```txt
aha status [--repo DIR] [--depot DEPOT] [--json]
```

**Flags:**

- `--config`
- `--corpus`
- `--depot`
- `--json`
- `--repo`

**Examples:**

- `aha status --json`
- `aha status --depot local:~/.aha/depot --json`

**JSON contract:** `object{corpus_dir,machines,sources,sessions,session_versions,entries,messages,artifacts,images,entry_assets,files,snapshots,conflicts,tool_invocations,fts_messages,fts_artifacts,session_path_tokens,artifact_path_tokens,index_size_bytes,depot_behind_snapshots?,depot_machines_listed?,depot_fetches?,next}`

## aha verify

verify corpus invariants and optionally repair derived FTS rows

```txt
aha verify [--repo DIR] [--repair-fts] [--progress MODE] [--json]
```

**Flags:**

- `--config`
- `--corpus`
- `--json`
- `--progress`
- `--repair-fts`
- `--repo`

**Examples:**

- `aha verify --json`
- `aha verify --repair-fts`

**JSON contract:** `object{root,stats,problems,repaired_fts,fts_repair?}`

