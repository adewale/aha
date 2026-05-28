# aha — Agent History Aggregator

`aha` is local search and retrieval for coding-agent history. It turns scattered Pi, Claude Code, and Codex transcripts into a private SQLite + FTS5 corpus with deterministic `tar.zst` bundles and agent-friendly refs.

Use it when agent conversations are becoming project memory and you want one local archive across tools and machines.

## Who is this for?

`aha` is for developers who use coding agents heavily and want past agent conversations to become searchable project memory.

It is especially useful if you:

- use multiple tools such as Pi, Claude Code, and Codex;
- work across multiple machines;
- need private local search over agent transcripts;
- want agents/scripts to retrieve prior context with stable JSON and refs;
- are currently using `rg`, ad hoc scripts, or tool-specific history search.

## What does it replace?

Many users start with:

- `rg` over `~/.claude/projects`, `~/.pi/agent/sessions`, or `~/.codex/sessions`;
- one-off JSONL parsing scripts;
- Claude-only history explorers;
- copy-pasted notes in `CLAUDE.md`, `AGENTS.md`, or project docs;
- asking an agent to rediscover old context.

`aha` turns those scattered histories into one local SQLite + FTS corpus with stable refs and full-context reads.

## Why use it?

- **One corpus for multiple agents**: Pi, Claude Code, and Codex today; more adapters later.
- **Private by default**: everything stays on your machine unless you explicitly configure a remote depot such as R2.
- **Portable history**: share a depot, copy a bundle from another machine, or `aha ingest` it.
- **Better than snippets**: search finds leads; read retrieves full context so humans and agents do not answer from snippets alone.
- **Agent-friendly retrieval**: JSON, refs, Markdown, and stable `search → read` workflows.
- **Auditable trust claims**: read-only source access, local-by-default behavior, and network boundaries are tested.

## Privacy warning

V1 does **not** redact secrets. Bundles and corpora may contain prompts, source code, tool output, credentials pasted into chat, images, paths, and API responses. Treat them as private.

See `docs/trust.md` for the trust model and verification commands.

## Install / build

Requires Go from `go.mod` on macOS or Linux. Windows support is planned for v2.

```bash
git clone https://github.com/adewale/aha.git
cd aha
go install ./cmd/aha
```

If `aha` is not found, add Go's bin directory to your shell path:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
command -v aha
aha --version
```

Local build without installing:

```bash
go build -o /tmp/aha ./cmd/aha
/tmp/aha --version
```

Run tests:

```bash
go test ./...
```

## Quick start

See `docs/onboarding.md` for the verifiable first-run checklist, including install/PATH checks and optional R2 setup.

Local-first setup:

```bash
aha doctor --json
aha init --accept-secrets
aha refresh --max-sessions 1 --json
aha refresh --json
aha verify --json
```

Search and then read a returned ref. With `--refs`, copy the first field on a result line:

```bash
aha search "dynamic workflows" --refs
REF=$(aha search "dynamic workflows" --refs | awk 'NF { print $1; exit }')
aha read "$REF" --md
```

Agent-oriented flow:

```bash
aha search "migration bug" --json --limit 10
REF=$(aha search "migration bug" --refs | awk 'NF { print $1; exit }')
aha read "$REF" --json
```

Expected result: `search` returns matching messages/artifacts as leads; `read` returns surrounding transcript entries or artifact text as evidence.

## Search functionality

`aha search` is deterministic local full-text search backed by SQLite FTS5. It indexes user/assistant text, summaries, and text artifacts while preserving raw source files for later reads.

Search supports:

- full-text queries over messages and artifacts;
- filters: `--source`, `--machine`, `--role`, `--after`, `--before`, `--path`, `--limit`;
- output modes: human text, `--json`, `--refs`, `--files`, `--md`;
- stable result identity: structured `ref` plus copy-pastable `ref_text`;
- literal flag-looking queries with `--`, e.g. `aha search -- --json`;
- coherent retrieval: every search hit should be readable with `aha read <ref>`.

What it does **not** do yet: semantic/vector search, ranking beyond SQLite FTS scoring plus deterministic ordering, OCR/image caption search, branch/thread reconstruction, or advanced query UI.

### Compared with Claude History Explorer and QMD

| Tool | Search scope | Search engine | Retrieval style | Best fit |
|---|---|---|---|---|
| `aha` | Pi + Claude Code + Codex, across machines after ingest | SQLite FTS5 over a local corpus | Search returns refs; `read <ref>` expands to full context/artifact text | Private cross-agent archive and agent-friendly retrieval |
| Claude History Explorer | Claude Code history only | On-demand parsing/regex-style local exploration | Browse/search Claude sessions directly | Lightweight Claude-only exploration |
| QMD-style workflows | Usually document/session search with agent-oriented outputs | Depends on QMD setup | Treat snippets as leads, then retrieve cited context | Query/read discipline and citation-like workflows |

`aha` borrows the QMD lesson that snippets are leads, not evidence: agents should search, read the returned refs, then answer from retrieved context. It differs from Claude History Explorer by snapshotting immutable multi-source bundles and merging them into a reusable local corpus instead of searching one live Claude tree on demand.

## Core journeys

### First local archive

```bash
aha init --accept-secrets
aha refresh
```

`refresh` is the short path: snapshot configured sources, reuse an unchanged depot bundle when state metadata proves nothing changed, then ingest pending/new bundles into the configured corpus.

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
aha [--cpuprofile FILE] [--memprofile FILE] <command> [args]
aha init [--config PATH] [--accept-secrets] [--json]
aha refresh [--session MATCH ...] [--max-sessions N] [--repo DIR] [--depot DEPOT] [--json]
aha snapshot [--session MATCH ...] [--max-sessions N] [--depot DEPOT] [--json]
aha ingest [--repo DIR] [--depot DEPOT] [--json] [bundle.tar.zst ...]
aha search <query> [--repo DIR] [--source NAME] [--machine ID] [--role ROLE] [--project KEY] [--path-token TOKEN] [--json|--refs|--files|--md]
aha read [REF] [--session ID] [--entry ID] [--repo DIR] [--before N] [--after N] [--json|--md]
aha status [--repo DIR] [--depot DEPOT] [--json]
aha verify [--repo DIR] [--repair-fts] [--json]
aha conflicts [--repo DIR] [--json]
aha corpus <size|vacuum|prune-orphans> [--repo DIR] [--json] [--force]
aha depot <init|ls|verify|compact> [DEPOT] [--json] [--repair] [--deep]
aha doctor [--depot DEPOT] [--json]
aha mcp [--config PATH] [--repo DIR]
aha serve [--addr HOST:PORT] [--allow-remote] [--config PATH] [--repo DIR]
```

Command roles:

- `init`: write starter JSONC config and optionally persist privacy acknowledgement.
- `refresh`: common local update: snapshot or reuse an unchanged depot bundle, then ingest pending/new bundles.
- `snapshot`: create an immutable bundle and store it in the configured depot.
- `ingest`: merge bundles from paths or a depot into a corpus/repo.
- `search`: find messages/artifacts; use `--json` or `--refs` for agents/scripts. Prefer indexed `--project`/`--path-token` over contains-style `--path` for large corpora; requested limits above 200 are capped with a warning.
- `read`: retrieve full context from `--session/--entry` or a canonical `ref_text` emitted by `search` (`msg:v1:...`, `session:v1:...`, or `artifact:v1:...`).
- `status`: corpus counts and health.
- `verify`: corpus invariant checks and optional FTS repair.
- `conflicts`: quarantined merge conflicts.
- `corpus`: inspect corpus disk usage, run SQLite vacuum, or explicitly prune unreferenced blob files (`prune-orphans` is dry-run unless `--force`).
- `depot`: initialize, list, verify, or compact a local/R2 bundle depot; `depot verify` is quick by default, while `--deep` reads bundle bytes/manifests and `--repair` rebuilds catalogs.
- `doctor`: environment, config, source, corpus, depot, and next-action diagnostics.
- `mcp`: run a read-only stdio MCP server over the corpus so coding agents can call `search`, `read`, `status`, `verify`, `conflicts`, `corpus_size`, and `doctor` as JSON-RPC tools. See `docs/mcp-spec.md`.
- `serve`: run a read-only local dashboard on loopback (`127.0.0.1:18428` by default). Same tool surface as `mcp`, served as HTTP/JSON plus a minimal embedded UI. Pass `--allow-remote` (or set `AHA_ALLOW_REMOTE=1`) to bind elsewhere.

Optional profiling: any command can write local Go pprof profiles with `--cpuprofile FILE` and/or `--memprofile FILE` before or after the subcommand, or with `AHA_CPU_PROFILE`/`AHA_MEM_PROFILE`.

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
| Depot | `~/.aha/depot` local bundle store |
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
  "depot": { "type": "local", "location": "~/.aha/depot" },
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

- `CHANGELOG.md` — notable unreleased changes.
- `docs/commands.md` — generated command metadata, examples, and JSON contracts.
- `docs/user-journeys.md` — journeys and defaults.
- `docs/trust.md` — privacy/trust model and verification.
- `docs/onboarding.md` — verifiable local-first onboarding, troubleshooting, and optional R2 setup.
- `docs/r2-bucket-settings.md` — recommended R2 bucket, token, endpoint, and audit settings.
- `docs/architecture.md` — high-level architecture diagram and flows.
- `docs/mcp-spec.md` — read-only stdio MCP server spec and tool surface.
- `docs/agent-history-aggregator-spec.md` — full v1 spec.
- `docs/correctness-by-construction-spec.md` — refactor spec for correctness by construction (PBT, state-machine, and fuzz strategy).
- `docs/cbc-prior-art-improvements-spec.md` — prior-art-derived hardening requirements and implementation hooks.
- `docs/performance-audit.md` — current performance hotspots, benchmark plan, and optimization guardrails.
- `docs/performance-scalability-plan.md` — pathological benchmark results, profiling lessons, and scalability/longevity roadmap.
- `docs/performance-results.md` — latest benchmark capture, success metrics, regressions, and deferred performance work.
- `docs/refactor-metrics-and-go-audit.md` — before/after metrics, profiling, regression verification, and Go best-practices audit for the duplication-refactor pass.
- `docs/verification.md` — local/CI verification profiles, fuzzing, static guardrails, and mutation testing.
- `docs/eval-rubric.md` — rubric for future evals.
- `docs/eval-results.md` — latest basic eval results.
- `docs/audits/testing-and-abstractions-audit.md` — latest TDD/testing/abstraction audit.
- `docs/audits/docs-consistency-audit.md` — latest docs consistency audit.
- `docs/audits/code-duplication-audit.md` — current production-code duplication baseline and refactor watch list.
- `docs/lessons-learned.md` — rollback/reimplementation lessons.
- `docs/comparisons/claude-history-explorer.md` — what `aha` adopted from Claude History Explorer.

## License

MIT; see `LICENSE`.
