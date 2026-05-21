# aha — Agent History Aggregator

`aha` turns local coding-agent histories into a private, searchable corpus. It snapshots Pi, Claude Code, and Codex sessions into deterministic `tar.zst` bundles, ingests those bundles into SQLite + FTS5, and lets humans or agents search then read the original context.

Use it when agent conversations are becoming project memory and you want one local archive across tools and machines.

## Why use it?

- **One corpus for multiple agents**: Pi, Claude Code, and Codex today; more adapters later.
- **Private by default**: everything stays on your machine unless you move the bundle/corpus.
- **Portable history**: copy a bundle from another machine and `aha ingest` it.
- **Agent-friendly retrieval**: search emits JSON/refs; read retrieves full context so agents do not answer from snippets alone.
- **Auditable trust claims**: read-only source access and no-network behavior are tested.

## Privacy warning

V1 does **not** redact secrets. Bundles and corpora may contain prompts, source code, tool output, credentials pasted into chat, images, paths, and API responses. Treat them as private.

See `docs/trust.md` for the trust model and verification commands.

## Install / build

Requires Go from `go.mod` on macOS or Linux. Windows support is planned for v2.

```bash
git clone https://github.com/adewale/aha.git
cd aha
go build -o aha ./cmd/aha
./aha --version
```

Run tests:

```bash
go test ./...
```

## Quick start

Create a config, acknowledge the privacy model, snapshot local histories, and ingest them:

```bash
aha init --accept-secrets
aha refresh
```

Search:

```bash
aha search "dynamic workflows"
```

Read full context around a hit:

```bash
aha read --session <session-key> --entry <entry-id> --before 3 --after 5
```

Agent-oriented flow:

```bash
aha search "migration bug" --json --limit 10
aha search "migration bug" --refs
aha read '<session-key>#<entry-id>' --json
```

Expected result: `search` returns matching messages/artifacts; `read` returns surrounding transcript entries or artifact text.

## Core journeys

### First local archive

```bash
aha init --accept-secrets
aha refresh
```

`refresh` is the short path: snapshot configured sources and ingest the new bundle into the configured corpus.

### Routine update

```bash
aha refresh
```

### Inspect one recent/local session

```bash
aha refresh --max-sessions 1
aha search "needle" --refs
```

### Import another machine

```bash
aha ingest ~/Downloads/aha-sessions-work-mac.tar.zst
aha search "migration" --machine work-mac
```

More journey rationale: `docs/user-journeys.md`.

## Commands

```txt
aha init [--config PATH] [--accept-secrets] [--json]
aha refresh [--session MATCH ...] [--max-sessions N] [--repo DIR] [--json]
aha snapshot [--session MATCH ...] [--max-sessions N] [--out DIR] [--json]
aha ingest [--repo DIR] [--json] [bundle.tar.zst ...]
aha search <query> [--repo DIR] [--source NAME] [--machine ID] [--role ROLE] [--json|--refs|--files|--md]
aha read [REF] [--session ID] [--entry ID] [--repo DIR] [--before N] [--after N] [--json|--md]
aha status [--repo DIR] [--json]
aha conflicts [--repo DIR] [--json]
aha doctor [--json]
```

Command roles:

- `init`: write starter JSONC config and optionally persist privacy acknowledgement.
- `refresh`: common local update: `snapshot` then ingest the just-created bundle.
- `snapshot`: create an immutable bundle without touching a corpus.
- `ingest`: merge bundles into a corpus/repo.
- `search`: find messages/artifacts; use `--json` or `--refs` for agents/scripts.
- `read`: retrieve full context from `--session/--entry` or a `<session>#<entry>` ref.
- `status`: corpus counts and health.
- `conflicts`: quarantined merge conflicts.
- `doctor`: environment, config, adapter, and next-action diagnostics.

## Supported sources

| Source | Default root | Format |
|---|---|---|
| Pi | `~/.pi/agent/sessions` | JSONL session files |
| Claude Code | `~/.claude/projects` | JSONL project/session files, including `agent-*` subagents |
| Codex | `~/.codex/sessions` | JSONL rollout/session files |

A source is read-only during snapshot. Raw files are copied into the bundle and preserved for provenance.

## Defaults

| Setting | Default |
|---|---|
| Config | `~/.config/aha/config.jsonc` |
| Corpus | `~/.aha` |
| Bundle output | `~/agent-session-bundles` |
| Machine ID | sanitized local hostname |
| Tool output indexing | off |
| Redaction | none in v1 |

Config is JSONC; flags override config.

```jsonc
{
  "machine_id": "ade-mbp",
  "sources": [
    { "type": "claude-code", "root": "~/.claude/projects", "enabled": true },
    { "type": "codex", "root": "~/.codex/sessions", "enabled": true },
    { "type": "pi", "root": "~/.pi/agent/sessions", "enabled": true }
  ],
  "corpus_dir": "~/.aha",
  "bundle_out_dir": "~/agent-session-bundles",
  "include_subagents": true,
  "include_images": true,
  "index_tool_output": false,
  "redaction": "none-v1",
  "accept_secrets_warning": true
}
```

## Agent guidance

For coding agents using `aha`:

1. Use `aha search ... --json` or `--refs` to find leads.
2. Use `aha read <ref> --json` to retrieve full source context.
3. Answer from retrieved context, not from snippets alone.
4. Prefer read-only commands (`search`, `read`, `status`, `conflicts`, `doctor`) unless the user explicitly asks to snapshot/ingest.
5. Remember v1 does not redact secrets.

## Accepted v1 limits

- No secret redaction.
- No Windows support until v2.
- `include_images=false` suppresses normalized image assets/blobs, but raw bundles/session JSON may still contain embedded image bytes.
- `read` shows file-order context, not source-native branch/thread reconstruction.
- Tool output is preserved in raw files but not indexed by default.
- Conflict UX can improve.

## Project docs

- `docs/user-journeys.md` — journeys and defaults.
- `docs/trust.md` — privacy/trust model and verification.
- `docs/agent-history-aggregator-spec.md` — full v1 spec.
- `docs/eval-rubric.md` — rubric for future evals.
- `docs/eval-results.md` — latest basic eval results.
- `docs/lessons-learned.md` — rollback/reimplementation lessons.
- `docs/comparisons/claude-history-explorer.md` — what `aha` adopted from Claude History Explorer.

## License

No license file is currently included. Add one before broad external adoption.
