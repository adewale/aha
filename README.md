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

Create the aggregation corpus for this machine:

```bash
aha init --accept-secrets
aha refresh
```

Search it:

```bash
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
aha refresh
```

Rationale: first use should be safe and short. `init` writes visible defaults and records the privacy acknowledgement. `refresh` snapshots the configured sources and ingests the new bundle into the configured corpus.

### Routine refresh

```bash
aha refresh
```

Rationale: after setup, refreshing should be one command. `refresh` is `snapshot` plus ingest of the bundle it just created.

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
aha refresh [--session MATCH ...] [--max-sessions N] [--repo DIR]
aha snapshot [--session MATCH ...] [--max-sessions N] [--out DIR]
aha ingest [--repo DIR] [bundle.tar.zst ...]
aha search <query> [--repo DIR] [--source NAME] [--machine ID] [--role ROLE] [--json]
aha read --session ID [--entry ID] [--repo DIR] [--before N] [--after N] [--json]
aha status [--repo DIR] [--json]
aha conflicts [--repo DIR] [--json]
aha doctor
```

## Why separate commands exist

- `refresh`: one-command local update; creates the aggregation corpus on first run. Use `--session` or `--max-sessions` for 1-to-all local-session scope.
- `snapshot`: capture an immutable bundle without touching the corpus. Use `--session` or `--max-sessions` for scoped snapshots.
- `ingest`: merge copied or existing bundles into a corpus/repo.
- `search`: find matches in the corpus.
- `read`: show context around a search hit.
- `status`: inspect corpus counts and health.
- `conflicts`: inspect quarantined merge conflicts.
- `doctor`: show environment, config, and adapter information.
- `init`: optional config materialization; useful for changing defaults and acknowledging the privacy warning once.

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
