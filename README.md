# aha — Agent History Aggregator

`aha` snapshots your local Pi and Claude Code histories into immutable `tar.zst` bundles, ingests those bundles into a local SQLite corpus, and lets you search/read the combined history.

Use it when you want one private, searchable archive of agent sessions across tools and machines.

## What it does

- Reads Pi sessions from `~/.pi/agent/sessions`.
- Reads Claude Code sessions from `~/.claude/projects`.
- Writes deterministic snapshot bundles to `~/agent-session-bundles`.
- Ingests bundles into a local SQLite + FTS5 corpus at `~/.aha`.
- Preserves raw session files, artifacts, subagent sessions, and image prompt metadata.

## Privacy first

V1 does **not** redact secrets. Bundles may contain prompts, source code, tool output, credentials accidentally pasted into chat, images, paths, and API responses. Keep bundles and corpora private unless you have reviewed them.

See `docs/trust.md` for the trust model and verification commands.

## Build

```bash
go build -o aha ./cmd/aha
go test ./...
```

## Quick start

```bash
aha init --accept-secrets
aha snapshot
aha ingest
aha search "dynamic workflows"
```

Read around a result:

```bash
aha read --session <session-key> --entry <entry-id> --before 3 --after 5
```

## Common journeys

### First local archive

```bash
aha init --accept-secrets
aha snapshot
aha ingest
```

Rationale: the first run should be safe and short. `init` writes visible defaults, records the privacy acknowledgement, and leaves the bundle/corpus locations explicit in JSONC.

### Routine refresh

```bash
aha snapshot
aha ingest
```

Rationale: after setup, refreshing should not require remembering paths. `snapshot` writes a new bundle to the configured bundle directory; `ingest` with no arguments ingests bundles from that directory and skips duplicates.

### Import another machine

```bash
aha ingest ~/Downloads/aha-sessions-work-mac.tar.zst
aha search "migration" --machine work-mac
```

Rationale: copied bundles should merge into the same local corpus. Machine identity comes from the manifest, not the filename.

More journeys and default rationale: `docs/user-journeys.md`.

## Commands

```txt
aha init [--accept-secrets]
aha snapshot [--machine ID] [--source pi=PATH] [--source claude-code=PATH] [--out DIR]
aha ingest [bundle.tar.zst ...]
aha search <query> [--source NAME] [--machine ID] [--role ROLE] [--json]
aha read --session ID [--entry ID] [--before N] [--after N] [--json]
aha status [--json]
aha conflicts [--json]
aha doctor
```

## Defaults

| Setting | Default |
|---|---|
| Config | `~/.config/aha/config.jsonc` |
| Corpus | `~/.aha` |
| Bundle output | `~/agent-session-bundles` |
| Pi source | `~/.pi/agent/sessions` |
| Claude Code source | `~/.claude/projects` |
| Machine ID | sanitized local hostname, written into config by `aha init` |
| Tool output indexing | off |
| Redaction | none in v1 |

Config is JSONC. Flags override config.

```jsonc
{
  "machine_id": "ade-mbp",
  "machine_label": "Adewale MacBook Pro",
  "sources": [
    { "type": "pi", "root": "~/.pi/agent/sessions", "enabled": true },
    { "type": "claude-code", "root": "~/.claude/projects", "enabled": true }
  ],
  "corpus_dir": "~/.aha",
  "bundle_out_dir": "~/agent-session-bundles",
  "path_mode": "raw",
  "include_subagents": true,
  "include_images": true,
  "index_tool_output": false,
  "redaction": "none-v1",
  "accept_secrets_warning": true
}
```

## Accepted v1 limits

- No secret redaction.
- No Windows support until v2.
- Project grouping uses a simple derived key.
- `read` shows file-order context, not source-native branch/thread reconstruction.
- Tool output is preserved in raw files but not indexed by default.
- Conflict rows are quarantined; conflict search/display UX can improve later.

## More docs

- `docs/user-journeys.md` — journeys and defaults.
- `docs/trust.md` — privacy/trust model and verification.
- `docs/agent-history-aggregator-spec.md` — full v1 spec.
- `docs/lessons-learned.md` — rollback/reimplementation lessons.
- `docs/comparisons/claude-history-explorer.md` — what `aha` adopted from Claude History Explorer.
