# aha — Agent History Aggregator

`aha` is a local Go CLI that snapshots, ingests, merges, and searches coding-agent session histories.

V1 supports:

- Pi sessions from `~/.pi/agent/sessions`
- Claude Code sessions from `~/.claude/projects`
- deterministic `tar.zst` snapshot bundles
- SQLite + FTS5 corpus search
- image blob extraction for image-bearing prompts when image bytes are embedded in session JSON
- subagent session/artifact preservation

## Privacy warning

V1 does **not** redact secrets. Bundles may contain credentials, private prompts, source code, tool output, images, filesystem paths, and API responses. Do not upload bundles or corpora publicly unless you have reviewed them yourself.

## Build

```bash
go build -o aha .
go test ./...
go test -race ./...
```

## Quick start

```bash
# Optional: create JSONC config
aha init

# Snapshot local sessions. --accept-secrets is required in v1.
aha snapshot \
  --machine ade-mbp \
  --source pi=$HOME/.pi/agent/sessions \
  --source claude-code=$HOME/.claude/projects \
  --out ~/agent-session-bundles \
  --accept-secrets

# Ingest bundles into ~/.aha/corpus.db
aha ingest ~/agent-session-bundles/aha-sessions-*.tar.zst

# Search and read context
aha search "dynamic workflows"
aha read --session <session-id-or-key> --entry <entry-id> --before 3 --after 5

# Inspect corpus
aha status
aha conflicts
aha doctor
```

## Commands

```txt
aha snapshot
aha ingest
aha search
aha read
aha status
aha conflicts
aha doctor
aha init
```

All user-facing query/status commands support `--json` where useful.

## Config

Config is JSONC at `~/.config/aha/config.jsonc` by default.

```jsonc
{
  // Required unless supplied with --machine.
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
  "redaction": "none-v1"
}
```

CLI flags override config values.

## V1 notes

- Windows support is intentionally punted to v2.
- SQLite is the corpus/query engine; `aha` uses FTS5 and normal SQL indexes rather than a custom search engine.
- Deterministic bundles require identical input files plus pinned/generated capture metadata. Tests pin `captured_at` and `bundle_id`.
- Image prompt reconstruction is implemented for embedded/base64 image content and records source references for non-embedded images.
