# Architecture

`aha` is a local-first CLI that snapshots supported coding-agent histories, stores deterministic bundles, ingests them into a SQLite + FTS5 corpus, and exposes search/read workflows for humans and agents.

```text
                         ┌────────────────────────────┐
                         │          aha CLI            │
                         │        cmd/aha/main.go      │
                         └─────────────┬──────────────┘
                                       │
                                       ▼
┌─────────────────────────────────────────────────────────────────┐
│                         internal/cli                            │
│  Run / RunMain / JSON errors / command registry / FlagSpec       │
│                                                                 │
│  init ─ refresh ─ snapshot ─ ingest ─ search ─ read ─ status     │
└───────┬───────────┬───────────┬──────────┬──────────┬───────────┘
        │           │           │          │          │
        │           │           │          │          ▼
        │           │           │          │   ┌──────────────────┐
        │           │           │          │   │ internal/corpus   │
        │           │           │          │   │ read/status/conf  │
        │           │           │          │   └────────┬─────────┘
        │           │           │          │            │
        │           │           │          ▼            ▼
        │           │           │   ┌──────────────────────────────┐
        │           │           │   │       internal/search         │
        │           │           │   │ SQLite FTS5 messages/artifacts│
        │           │           │   └──────────────┬───────────────┘
        │           │           │                  │
        │           │           ▼                  ▼
        │           │   ┌──────────────────────────────────────────┐
        │           │   │              SQLite corpus                │
        │           │   │ corpus.db + FTS + blobs/files/images      │
        │           │   └──────────────────────────────────────────┘
        │           │
        │           ▼
        │   ┌──────────────────────────────────────────┐
        │   │            internal/archive               │
        │   │ Capture / deterministic tar.zst / validate│
        │   └──────────────┬───────────────────────────┘
        │                  │
        │                  ▼
        │   ┌──────────────────────────────────────────┐
        │   │          internal/adapters                │
        │   │ Pi / Claude Code / Codex discover+parse   │
        │   └──────────────┬───────────────────────────┘
        │                  │
        ▼                  ▼
┌────────────────┐  ┌─────────────────────────────────────────────┐
│ internal/config│  │          Local agent history roots           │
│ JSONC config   │  │ ~/.pi/...  ~/.claude/projects  ~/.codex/... │
└────────────────┘  └─────────────────────────────────────────────┘
```

## Main flows

```text
Snapshot path:
  config → adapters discover/parse → archive.Capture → deterministic tar.zst

Ingest path:
  tar.zst → archive validation → corpus ingest planner/blob publisher/writer
         → SQLite tables + FTS + content-addressed blobs

Query path:
  search → SQLite FTS → stable HitRef/ref_text
  read <ref> → corpus.ReadRef/ReadContext → transcript window or artifact text
```
