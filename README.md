# aha — Agent History Aggregator

`aha` is a local Go CLI that snapshots, ingests, merges, and searches coding-agent session histories.

V1 supports Pi and Claude Code sessions, deterministic `tar.zst` bundles, SQLite + FTS5 search, subagent/artifact preservation, and image blob extraction for embedded image-bearing prompts.

## Privacy warning

V1 does **not** redact secrets. Bundles may contain credentials, private prompts, source code, tool output, images, filesystem paths, and API responses. Do not upload bundles or corpora publicly unless you have reviewed them yourself.

## Build

```bash
go build -o aha ./cmd/aha
go test ./...
go test -race ./...
```

## Quick start

```bash
aha init

aha snapshot \
  --machine ade-mbp \
  --source pi=$HOME/.pi/agent/sessions \
  --source claude-code=$HOME/.claude/projects \
  --out ~/agent-session-bundles \
  --accept-secrets

aha ingest ~/agent-session-bundles/aha-sessions-*.tar.zst
aha search "dynamic workflows"
aha read --session <session-id-or-key> --entry <entry-id> --before 3 --after 5
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

## V1 notes and accepted limitations

- Windows support is intentionally punted to v2.
- V1 preserves raw data and does not redact secrets; bundles and corpora are private artifacts.
- Project grouping uses a simple derived key; configurable cross-machine path rewrite rules are v2+.
- Tool output is preserved in raw session files but is not indexed by default.
- `read` returns bounded file-order context; source-native branch/thread reconstruction is v2+.
- Conflict rows are quarantined and never overwrite existing entries; conflict search/display UX can improve later.
- SQLite is the corpus/query engine; `aha` uses FTS5 and SQL indexes rather than a custom search engine.
- Deterministic bundles require identical input files plus pinned/generated capture metadata. Tests pin `captured_at` and `bundle_id`.
- Image prompt reconstruction metadata is stored for embedded/base64 image content, including prompt order, source reference, MIME type, hash, and dimensions when available.
- See `docs/lessons-learned.md` for the rollback/reimplementation lessons that shaped v1.
