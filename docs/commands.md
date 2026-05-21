# aha commands

This file is generated from CLI command metadata. Update command metadata, then regenerate this file.

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

## aha doctor

show diagnostics and next actions

```txt
aha doctor [--json]
```

**Flags:**

- `--json`

**Examples:**

- `aha doctor`

**JSON contract:** `object{version,config,adapters,next}`

## aha ingest

merge one or more bundles into a corpus

```txt
aha ingest [--repo DIR] [bundle.tar.zst ...]
```

**Flags:**

- `--config`
- `--corpus`
- `--repo`
- `--json`

**Examples:**

- `aha ingest ./bundle.tar.zst`
- `aha ingest --repo ./aha-repo`

**JSON contract:** `array<object{bundle,sessions,entries,messages,images,artifacts,duplicate}>`

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

- `aha read <session>#<entry> --json`
- `aha read --session <session> --entry <entry> --json`

**JSON contract:** `array<object{line_no,entry_id,timestamp,role,text,raw_json}>`

## aha refresh

snapshot configured sources and ingest the new bundle

```txt
aha refresh [--session MATCH ...] [--max-sessions N] [--repo DIR] [--json]
```

**Flags:**

- `--accept-secrets`
- `--bundle-id`
- `--captured-at`
- `--config`
- `--corpus`
- `--machine`
- `--max-sessions`
- `--out`
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
aha search <query> [--repo DIR] [--source NAME] [--machine ID] [--role ROLE] [--json|--refs|--files|--md]
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
- `--refs`
- `--repo`
- `--role`
- `--source`

**Examples:**

- `aha search needle --json`
- `aha search needle --refs`

**JSON contract:** `array<object{score,timestamp,source,machine,project,role,snippet,session_key,entry_id,ref}>`

## aha snapshot

create an immutable local history bundle

```txt
aha snapshot [--session MATCH ...] [--max-sessions N] [--out DIR] [--json]
```

**Flags:**

- `--accept-secrets`
- `--bundle-id`
- `--captured-at`
- `--config`
- `--machine`
- `--max-sessions`
- `--out`
- `--session`
- `--source`
- `--json`

**Examples:**

- `aha snapshot --accept-secrets --out ./bundles`

**JSON contract:** `object{bundle,sha256,bundle_id,captured_at}`

## aha status

summarize corpus health

```txt
aha status [--repo DIR] [--json]
```

**Flags:**

- `--config`
- `--corpus`
- `--json`
- `--repo`

**Examples:**

- `aha status --json`

**JSON contract:** `object{corpus_dir,sessions,entries,messages,artifacts,images,bundles,conflicts,index_size_bytes,next}`

