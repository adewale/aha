# aha — Agent History Aggregator

`aha` is a local, cross-agent, cross-machine archive of your coding-agent history — the substrate for examining your own behaviour and (eventually) turning those patterns into better skills, prompts, and workflows.

It captures Pi, Claude Code, Codex, and OpenCode sessions from every machine you work on into a single private SQLite + FTS5 corpus with deterministic `tar.zst` bundles and stable agent-friendly refs. Browse it in a local dashboard, search it from the CLI, or wire it up so your own coding agents can read the depot. Pattern detection and skill-candidate generation are documented as the next layer; they're not in the box today.

Use it when you've accumulated enough coding-agent conversations that you want to understand how you and your agents work — not just re-find snippets.

## Who is this for?

`aha` is for developers who:

- use multiple coding agents (Pi, Claude Code, Codex, OpenCode — more adapters later) and want one place to examine everything they've done;
- work across multiple machines and want a portable, content-addressed history that follows them;
- want to *manually* find patterns in their own prompts and agent behaviour today, and want the substrate that automated pattern detection will be built on tomorrow;
- want their agents to *read* the depot today via MCP, with the "agent proposes new skills" loop tracked but not yet built;
- need private local search over agent transcripts;
- are currently using `rg`, ad hoc scripts, or tool-specific history search.

## What ships today

The substrate is built. The pattern-detection layer the substrate is for is documented, not implemented:

- **Local dashboard** (`aha serve`) — a loopback web UI for browsing search results and reading full session context. Today: search box, results list, read pane, status strip, conflicts panel. Tomorrow (tracked in `docs/research/agent-trace-tools.md`): recurring-command rollups, retried-prompt views, costly-loop detection. Today's view is "browse"; tomorrow's view is "diagnose".
- **Read-only MCP server** (`aha mcp`) — coding agents can call `search`, `read`, `status`, `verify`, `conflicts`, `corpus_size`, and `doctor` as JSON-RPC tools. An agent can ask your history "have I asked this before?" or "which prompts do I keep repeating?" *via search queries you write*. The agent can't yet author skills from what it finds — that's the next layer.
- **Typed TypeScript client** (`clients/typescript/`) — code-mode agent runtimes (Cloudflare codemode, Anthropic code-execution-with-MCP) can fan out (`search → filter → Promise.all(read)`) over the corpus in one round trip. See `clients/typescript/README.md` for concrete examples.

The longer-term direction is the pattern-detection layer in `docs/research/agent-trace-tools.md`: skill-candidate detection, recurring-failure rollups, cross-machine "what was I doing last Tuesday across all my agents". Nothing in there ships in this release.

## What does it replace?

Many users start with:

- `rg` over `~/.claude/projects`, `~/.pi/agent/sessions`, or `~/.codex/sessions`;
- one-off JSONL parsing scripts;
- Claude-only history explorers;
- copy-pasted notes in `CLAUDE.md`, `AGENTS.md`, or project docs;
- asking an agent to rediscover old context.

`aha` turns those scattered histories into one local SQLite + FTS corpus with stable refs and full-context reads.

## Why use it?

- **One corpus for multiple agents on multiple machines**: Pi, Claude Code, Codex, and OpenCode today across every machine you work from; more adapters later.
- **Built for pattern-finding**: a stable schema and a typed retrieval surface so you (or an agent on your behalf) can ask "what do I keep doing", "where did this go wrong before", "have I asked this already".
- **Private by default**: everything stays on your machine unless you explicitly configure a remote depot such as R2.
- **Portable history**: deterministic `tar.zst` bundles + a local-or-R2 depot; share a depot, copy a bundle from another machine, or `aha ingest` it.
- **Better than snippets**: search finds leads; `read` retrieves full context so humans and agents do not answer from fragments.
- **Agent-friendly retrieval**: JSON, refs, Markdown, a read-only MCP server, and a typed TypeScript client for code-mode runtimes.
- **Auditable trust claims**: read-only source access, local-by-default behaviour, and network boundaries are tested.

## Privacy warning

V1 does **not** redact secrets. Bundles and corpora may contain prompts, source code, tool output, credentials pasted into chat, images, paths, and API responses. Treat them as private.

Redaction at ingest is the next planned change; see `docs/redaction-spec.md` for the design and `docs/trust.md` for the current trust model and verification commands.

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
| `aha` | Pi + Claude Code + Codex + OpenCode, across machines after ingest | SQLite FTS5 over a local corpus | Search returns refs; `read <ref>` expands to full context/artifact text | Private cross-agent archive and agent-friendly retrieval |
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
aha depot <init|use|ls|verify|compact> [DEPOT] [--json] [--repair] [--deep]
aha doctor [--depot DEPOT] [--json]
aha mcp [--config PATH] [--repo DIR] [--dry-run]
aha serve [--addr HOST:PORT] [--allow-remote] [--allowed-hosts H1,H2] [--timeout DUR] [--token TOKEN] [--config PATH] [--repo DIR]
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
- `depot`: initialize, switch the default (`use`), list, verify, or compact a local/R2 bundle depot; `depot verify` is quick by default, while `--deep` reads bundle bytes/manifests and `--repair` rebuilds catalogs.
- `doctor`: environment, config, source, corpus, depot, and next-action diagnostics.
- `mcp`: run a read-only stdio MCP server over the corpus so coding agents can call `search`, `read`, `status`, `verify`, `conflicts`, `corpus_size`, and `doctor` as JSON-RPC tools. `--dry-run` opens the corpus, registers the tools, prints a one-line summary, and exits — a pre-flight check for host wiring. See `docs/mcp-spec.md`.
- `serve`: run a read-only local dashboard on loopback (`127.0.0.1:18428` by default). Same tool surface as `mcp`, served as HTTP/JSON plus a minimal embedded UI. Loopback binds need no auth; passing `--allow-remote` (or setting `AHA_ALLOW_REMOTE=1`) requires a shared-secret bearer token via `--token` (or `AHA_DASHBOARD_TOKEN`). Hostnames accepted via `Host:` are restricted to the loopback allowlist by default; extend with `--allowed-hosts`.

Optional profiling: any command can write local Go pprof profiles with `--cpuprofile FILE` and/or `--memprofile FILE` before or after the subcommand, or with `AHA_CPU_PROFILE`/`AHA_MEM_PROFILE`.

## Supported sources

| Source | Default root | Format |
|---|---|---|
| Pi | `~/.pi/agent/sessions` | JSONL session files |
| Claude Code | `~/.claude/projects` | JSONL project/session files, including `agent-*` subagents |
| Codex | `~/.codex/sessions` | JSONL rollout/session files |
| OpenCode | `$XDG_DATA_HOME/opencode` when set, otherwise `~/.local/share/opencode` | SQLite database (`opencode.db`), converted to JSONL during discovery |

A source is read-only during snapshot. For JSONL sources, raw files are copied into the bundle and preserved for provenance. OpenCode's SQLite database is converted to deterministic, lossless JSONL during discovery — the original `data` JSON of every `session`/`message`/`part` row is preserved verbatim, and the bundle stores those JSONL files. The source database is copied (with any WAL/SHM sidecars that are present) into a private export cache before reading and is never written to. `$OPENCODE_DB` is an exclusive database-path override; when it is not set, release-channel databases (`opencode-*.db`) beside the default are picked up automatically. The export cache defaults under the user cache directory (`.../aha/opencode-export/<db-hash>/`), uses private directory/file modes, and can be redirected with `AHA_OPENCODE_EXPORT_DIR` or size-limited with `AHA_OPENCODE_MAX_DB_BYTES`.

### Verifying an adapter against a real machine

`scripts/smoketest.sh <opencode|codex|claude|pi> [SOURCE_ROOT]` runs a safe end-to-end check (discovery → snapshot → ingest → search → read) against your real history. Every artifact it generates goes under a single `/tmp` directory — a throwaway corpus, depot, config, cache/build cache, and (for OpenCode) the JSONL export — so your real `~/.aha`/`~/.config/aha` are untouched and there is nothing to clean up. It fingerprints and content-hashes source files before and after (plus `integrity_check` of OpenCode databases) and fails if anything changed, giving a strong read-only regression check.

```bash
scripts/smoketest.sh opencode          # uses the default root
scripts/smoketest.sh codex ~/.codex/sessions
```

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
    { "type": "opencode", "root": "~/.local/share/opencode", "enabled": true },
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
4. Prefer query-only commands (`search`, `read`, `status`, `conflicts`) unless the user explicitly asks to snapshot/ingest. `doctor` is diagnostic but may create/update the private OpenCode JSONL export cache while counting OpenCode sessions.
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
- `docs/depot-lifecycle.md` — depot states, transitions, and how the default depot is chosen and switched.
- `docs/r2-bucket-settings.md` — recommended R2 bucket, token, endpoint, and audit settings.
- `docs/architecture.md` — high-level architecture diagram and flows.
- `docs/mcp-spec.md` — read-only stdio MCP server spec and tool surface.
- `docs/redaction-spec.md` — secret-redaction design (v1.1, in design).
- `docs/research/agent-trace-tools.md` — neighbour-tool analysis (Tracebase, Self-Care, claude-session-analyzer, agenttrace, skill-optimizer, Crune, retrospective-skill, claude-history, plus broader survey).
- `docs/research/openinference.md` — OpenInference semantic-convention reference.
- `docs/research/openinference-impact-estimate.md` — data-size and performance estimate for adopting OpenInference's schema.
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
