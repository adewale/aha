# aha commands

This file is generated from CLI command metadata. Update command metadata, then regenerate this file.

## Global profiling

Any command may write Go pprof profiles with `--cpuprofile FILE` and/or `--memprofile FILE`. These flags can appear before or after the subcommand, or be supplied via `AHA_CPU_PROFILE` and `AHA_MEM_PROFILE`. Profiles are local debugging artefacts and are not written unless explicitly requested.

Examples: `aha --cpuprofile cpu.pprof search needle`, `aha workspace verify --memprofile heap.pprof`.

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
    "next": ["aha status --json"],
    "next_action": {"command": "aha", "args": ["status", "--json"]},
    "diagnostics": []
  }
}
```

With `--progress=json`, stderr remains valid NDJSON: progress events are followed by one terminal `aha.error.v1` object on failure.

## aha analyse

rank recurring tool-call failures and the resolution paths that worked

```txt
aha analyse failures [--workspace PATH] [--limit N] [--state S] [--project P] [--source S] [--machine M] [--tool T] [--json]
```

**Flags:**

- `--config`
- `--json`
- `--limit`
- `--machine`
- `--project`
- `--source`
- `--state`
- `--tool`
- `--workspace`

**Examples:**

- `aha analyse failures --json`
- `aha analyse failures --state unresolved`

**JSON contract:** `array<object{tool_name,command_family,error_signature,episodes,distinct_sessions,distinct_projects,resolved,resolution_rate,state,tier,first_seen,last_seen,spark,paths,sample_ref,score}>`

## aha archive

manage durable aggregated history and explicit upload/download transitions

```txt
aha archive <init|set-default|status|upload|download|verify> [ARCHIVE] [--workspace PATH] [--deep] [--dry-run] [--progress MODE] [--json]
```

**Flags:**

- `--config`
- `--deep`
- `--dry-run`
- `--force`
- `--json`
- `--progress`
- `--workspace`

**Examples:**

- `aha archive init`
- `aha archive upload`
- `aha archive download`
- `aha archive status r2:team-history --json`
- `aha archive verify --deep`

**JSON contract:** `aha.archive.status.v2|object{state,...}`

## aha dashboard

run the read-only local dashboard over a Workspace

```txt
aha dashboard [--workspace PATH] [--addr HOST:PORT] [--allow-remote] [--allowed-hosts H1,H2] [--timeout DUR] [--token TOKEN]
```

**Flags:**

- `--addr`
- `--allow-remote`
- `--allowed-hosts`
- `--config`
- `--timeout`
- `--token`
- `--workspace`

**Examples:**

- `aha dashboard`
- `aha dashboard --addr 127.0.0.1:18428`

**JSON contract:** `http://HOST:PORT/api/v2/{search,show,analyse,overview,status,workspace}`

## aha init

write config, assign the machine, initialise the default local Archive, and prepare the default Workspace destination

```txt
aha init --acknowledge-raw-history [--config PATH] [--dry-run] [--json]
```

**Flags:**

- `--acknowledge-raw-history`
- `--config`
- `--dry-run`
- `--json`

**Examples:**

- `aha init --acknowledge-raw-history`

**JSON contract:** `object{config,acknowledged_raw_history,archive,workspace}`

## aha mcp

check or serve the read-only stdio MCP interface over a Workspace

```txt
aha mcp <check|serve> [--workspace PATH] [--config PATH]
```

**Flags:**

- `--config`
- `--workspace`

**Examples:**

- `aha mcp check`
- `aha mcp serve`

**JSON contract:** `check diagnostic|jsonrpc:tools/list|tools/call`

## aha search

find relevant messages and artefacts; use show on returned refs before answering

```txt
aha search [--workspace PATH] QUERY [--source NAME] [--machine ID] [--role ROLE] [--project KEY] [--path-token TOKEN] [--json|--refs|--files|--md]
```

**Flags:**

- `--after`
- `--before`
- `--config`
- `--files`
- `--json`
- `--limit`
- `--machine`
- `--md`
- `--path`
- `--path-token`
- `--project`
- `--refs`
- `--role`
- `--source`
- `--workspace`

**Examples:**

- `aha search needle --json`
- `aha search needle --refs`

**JSON contract:** `array<object{score,timestamp,source,machine,project,role,snippet,session_key,entry_id,ref,ref_text}>`

## aha show

display contextual evidence for a search result

```txt
aha show [--workspace PATH] REF [--session ID] [--entry ID] [--before N] [--after N] [--json|--md]
```

**Flags:**

- `--after`
- `--before`
- `--branch`
- `--config`
- `--entry`
- `--json`
- `--live`
- `--md`
- `--session`
- `--workspace`

**Examples:**

- `aha show <ref_text> --json`
- `aha show --session <session> --entry <entry> --json`

**JSON contract:** `array<object{line_no,entry_id,timestamp,role,text,raw_json}>`

## aha status

inspect agent-history, Archive, and Workspace state with one next transition

```txt
aha status [--archive ARCHIVE] [--workspace PATH] [--json]
```

**Flags:**

- `--archive`
- `--config`
- `--json`
- `--workspace`

**Examples:**

- `aha status --json`
- `aha status --archive r2:team-history --workspace ~/.aha/workspace --json`

**JSON contract:** `aha.status.v2`

## aha workspace

select, inspect, verify, repair, or inspect conflicts in a local Workspace

```txt
aha workspace <set-default|status|verify|repair|conflicts> [PATH] [--repair-fts] [--backup] [--dry-run] [--progress MODE] [--json]
```

**Flags:**

- `--backup`
- `--config`
- `--dry-run`
- `--json`
- `--progress`
- `--repair-fts`

**Examples:**

- `aha workspace status`
- `aha workspace verify --repair-fts`
- `aha workspace repair --backup`
- `aha workspace conflicts --json`

**JSON contract:** `aha.workspace.status.v2|object|array`

