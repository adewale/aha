# aha commands

This file is generated from CLI command metadata. Update command metadata, then regenerate this file.

## Global profiling

Any command may write Go pprof profiles with `--cpuprofile FILE` and/or `--memprofile FILE`. These flags can appear before or after the subcommand, or be supplied via `AHA_CPU_PROFILE` and `AHA_MEM_PROFILE`. Profiles are local debugging artifacts and are not written unless explicitly requested.

Examples: `aha --cpuprofile cpu.pprof search needle`, `aha verify --memprofile heap.pprof`.

## JSON errors

When a command is invoked with `--json`, failures are written to stderr as:

```json
{
  "error": {
    "code": "machine_readable_code",
    "message": "human-readable message",
    "command": "command-name",
    "next": ["aha doctor"]
  }
}
```

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

inspect corpus disk usage, vacuum SQLite, or explicitly prune unreferenced blobs

```txt
aha corpus <size|vacuum|prune-orphans> [--repo DIR] [--json] [--force]
```

**Flags:**

- `--config`
- `--corpus`
- `--force`
- `--json`
- `--repo`

**Examples:**

- `aha corpus size --json`
- `aha corpus vacuum`
- `aha corpus prune-orphans --json`

**JSON contract:** `object{root,total_bytes,database_bytes,bundle_blob_bytes,file_blob_bytes,image_blob_bytes,other_bytes,files}|object{before_bytes,after_bytes,reclaimed_bytes}|object{root,dry_run,orphan_bytes,deleted_files,deleted_bytes,orphans}`

## aha depot

initialize, list, verify, or compact a bundle depot

```txt
aha depot <init|ls|verify|compact> [DEPOT] [--json] [--repair] [--deep]
```

**Flags:**

- `--config`
- `--deep`
- `--json`
- `--repair`

**Examples:**

- `aha depot init local:~/.aha/depot`
- `aha depot ls --json`
- `aha depot verify --deep`
- `aha depot verify --repair`
- `aha depot compact --json`

**JSON contract:** `object|array`

## aha doctor

show diagnostics and next actions

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

**JSON contract:** `object{version,config,adapters,sources,corpus,depot,next}`

## aha ingest

merge one or more bundles into a corpus

```txt
aha ingest [--repo DIR] [--depot DEPOT] [--json] [bundle.tar.zst ...]
```

**Flags:**

- `--config`
- `--corpus`
- `--depot`
- `--repo`
- `--json`

**Examples:**

- `aha ingest ./bundle.tar.zst`
- `aha ingest --repo ./aha-repo`
- `aha ingest --depot local:~/.aha/depot`

**JSON contract:** `array<object{bundle,sha256?,bytes?,fetched?,sessions,entries,messages,images,artifacts,duplicate}>`

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
aha mcp [--config PATH] [--repo DIR]
```

**Flags:**

- `--config`
- `--corpus`
- `--repo`

**Examples:**

- `aha mcp`

**JSON contract:** `jsonrpc:tools/list|tools/call (stdio MCP)`

## aha read

retrieve source context for a search result

```txt
aha read [REF] [--session ID] [--entry ID] [--repo DIR] [--before N] [--after N] [--json|--md]
```

**Flags:**

- `--after`
- `--before`
- `--config`
- `--corpus`
- `--entry`
- `--json`
- `--md`
- `--repo`
- `--session`

**Examples:**

- `aha read <ref_text> --json`
- `aha read --session <session> --entry <entry> --json`

**JSON contract:** `array<object{line_no,entry_id,timestamp,role,text,raw_json}>`

## aha refresh

snapshot configured source state or reuse unchanged depot state, then ingest pending/new depot bundles

```txt
aha refresh [--session MATCH ...] [--max-sessions N] [--repo DIR] [--depot DEPOT] [--json]
```

**Flags:**

- `--accept-secrets`
- `--bundle-id`
- `--captured-at`
- `--config`
- `--corpus`
- `--depot`
- `--machine`
- `--max-sessions`
- `--repo`
- `--session`
- `--source`
- `--json`

**Examples:**

- `aha refresh`
- `aha refresh --session abc --max-sessions 1`

**JSON contract:** `object{bundle,sha256,report}`

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

**JSON contract:** `http://HOST:PORT/api/{search,read,status,verify,conflicts,corpus_size,doctor}`

## aha snapshot

create an immutable local history bundle and store it in a depot

```txt
aha snapshot [--session MATCH ...] [--max-sessions N] [--depot DEPOT] [--json]
```

**Flags:**

- `--accept-secrets`
- `--bundle-id`
- `--captured-at`
- `--config`
- `--depot`
- `--machine`
- `--max-sessions`
- `--session`
- `--source`
- `--json`

**Examples:**

- `aha snapshot --accept-secrets --depot local:./bundles`

**JSON contract:** `object{bundle,sha256,bundle_id,captured_at}`

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

**JSON contract:** `object{corpus_dir,machines,sources,sessions,session_versions,entries,messages,artifacts,images,entry_assets,files,bundles,conflicts,fts_messages,fts_artifacts,session_path_tokens,artifact_path_tokens,index_size_bytes,depot_behind_bundles?,depot_catalog_refs_listed?,depot_unique_refs_listed?,depot_fetches?,next}`

## aha verify

verify corpus invariants and optionally repair derived FTS rows

```txt
aha verify [--repo DIR] [--repair-fts] [--json]
```

**Flags:**

- `--config`
- `--corpus`
- `--json`
- `--repair-fts`
- `--repo`

**Examples:**

- `aha verify --json`
- `aha verify --repair-fts`

**JSON contract:** `object{root,stats,problems,repaired_fts,fts_repair?}`

