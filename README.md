# aha — Agent History Aggregator

`aha` is a local, cross-agent, cross-machine archive of your coding-agent history — the substrate for examining your own behaviour and (eventually) turning those patterns into better skills, prompts, and workflows.

It captures Pi, Claude Code, Codex, and OpenCode sessions from every machine you work on into a single private SQLite + FTS5 corpus with content-addressed depot snapshots and stable agent-friendly refs. Browse it in a local dashboard, search it from the CLI, or wire it up so your own coding agents can read the depot. `aha incidents` surfaces recurring tool-call failures alongside the fixes that resolved them; it does not write skills for you.

Use it when you've accumulated enough coding-agent conversations that you want to understand how you and your agents work — not just re-find snippets.

## Who is this for?

`aha` is for developers who:

- use multiple coding agents (Pi, Claude Code, Codex, OpenCode — more adapters later) and want one place to examine everything they've done;
- work across multiple machines and want a portable, content-addressed history that follows them;
- want to find patterns in their own prompts, agent behaviour, and recurring tool-call failures;
- want their agents to *read* the depot today via MCP while keeping skill authoring an explicit human/agent workflow outside `aha`;
- need private local search over agent transcripts;
- are currently using `rg`, ad hoc scripts, or tool-specific history search.

## What ships today

The substrate is built, with a first deterministic pattern layer for recurring tool failures:

- **Local dashboard** (`aha serve`) — a loopback web UI with a corpus overview, search/read, and a unified **incidents** view (recurring failures with their resolution status, sparklines, fix paths, trajectory drill-in, and a copy-fix-notes affordance). It is still a browser over local data, not an autonomous runbook/skill/workflow author.
- **Read-only MCP server** (`aha mcp`) — coding agents can call `search`, `read`, `incidents`, `incident_trajectory`, `overview`, `status`, `verify`, `conflicts`, `corpus_size`, and `doctor` as JSON-RPC tools. An agent can ask your history "have I asked this before?", "which failures keep recurring unresolved?", or "show me the fix that worked" via read-only tools.
- **Typed TypeScript client** (`clients/typescript/`) — code-mode agent runtimes (Cloudflare codemode, Anthropic code-execution-with-MCP) can run one code-mode program over a long-lived transport (`search → filter → Promise.all(read)`). That is still multiple MCP tool calls when the program fans out. See `clients/typescript/README.md` for examples.
- **Incidents** (`aha incidents`) — recurring tool-call failures grouped by tool, command family, and normalized error signature, each carrying both its recurrence (episodes, distinct sessions/projects, first/last seen) and its resolution status: `unresolved` / `partial` / `resolved`, a tier, and the top **resolution paths that actually worked** (ranked by a Wilson lower bound so a one-off fix never outranks a repeatedly-confirmed one). Filter `--state unresolved` for the unsolved-pain to-do list or `--state resolved` for fixes worth harvesting. Incidents are signals for humans/agents to choose a runbook, skill, dynamic workflow, tool/platform fix, or investigation item; `aha` does not generate or install those artifacts. See `docs/outcome-weighting-spec.md` and `docs/patterns-and-interventions.md`.

The longer-term direction is tracked in `docs/research/agent-trace-tools.md`: broader incident-to-skill discovery, retried-prompt views, costly-loop detection, and cross-machine "what was I doing last Tuesday across all my agents".

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
- **Portable history**: content-addressed snapshots in a local-or-R2 depot — unchanged files never upload twice; share a depot, or hand someone one file via `aha export` / `aha ingest`.
- **Better than snippets**: search finds leads; `read` retrieves full context so humans and agents do not answer from fragments.
- **Agent-friendly retrieval**: JSON, refs, Markdown, a read-only MCP server, and a typed TypeScript client for code-mode runtimes.
- **Auditable trust claims**: read-only source access, local-by-default behaviour, and network boundaries are tested.

## Use it like this

- Run `aha refresh` to keep your local corpus current.
- Use `aha search ...` to find leads.
- Use `aha read <ref>` before trusting a result.
- Use `aha serve` when you want a visual trace/evidence browser.
- Use `aha incidents` to find recurring failures and observed fix paths.
- Classify recurring patterns into runbooks, skills, dynamic workflows, platform fixes, or backlog items.
- Expose `aha mcp` when you want agents to query the archive directly.

## Privacy warning

By default, `aha` preserves today's `none-v1` behavior: depot snapshots and corpora may contain prompts, source code, tool output, credentials pasted into chat, images, paths, and API responses. Treat them as private.

Set `"redaction":"v1"` to redact known secret patterns from corpus projections (`messages`, `tool_invocations`, `entries.raw_json`, artifacts, and FTS) at ingest. Depot blobs remain raw provenance. See `docs/redaction-spec.md` and `docs/trust.md`.

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

## Long-running command progress

`refresh`, `snapshot`, `ingest`, `verify`, `depot verify`, and `corpus rebuild`
emit typed phase progress on **stderr**. Final command output remains on stdout:

- terminal use: one updating line with phase, honest counters/percentages when a
  total is known, and elapsed time;
- redirected non-JSON use: stable phase-start/completion lines;
- `--json` in a terminal: progress remains visible on stderr while stdout stays
  one valid final JSON document;
- redirected `--json`: progress defaults off so scripts remain quiet;
- `--progress=json`: NDJSON progress events on stderr for automation;
- `--progress=off|plain|tty|json|auto` overrides selection explicitly.

No ETA is shown: totals are reported only when known before completion. Example:

```bash
aha depot verify --deep --json --progress=json \
  >verify-result.json 2>verify-progress.ndjson
```

Ctrl-C propagates cancellation through capture, depot upload/pull, deep blob
verification, and rebuild phase boundaries. Operations remain idempotent; a
rebuild swaps directories only after its replacement verifies cleanly.

## Errors and diagnostics

Every `aha` command, MCP tool, and dashboard API failure crosses the same safe
error boundary. Default output contains one concise error and exactly one next
action; raw SDK, SQL, filesystem-path, and dependency strings are retained only
as internal causes and are never rendered. `--json` uses the versioned
`aha.error.v1` envelope with a structured `next_action`.

Use global `--verbose-errors` to add allowlisted diagnostics—failure kind,
operation, and retryability—not raw causal text or credentials. When combined
with `--progress=json`, stderr is valid NDJSON through the terminal error.

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

## From patterns to interventions

`aha incidents` does not assume every recurring pattern should become a skill. Use incidents as evidence, then choose the right artifact:

| Pattern shape | Prefer |
|---|---|
| Repeatable command/check sequence | Runbook |
| Reusable judgment or habit | Skill |
| Broad, parallel, uncertain work | Dynamic workflow |
| Repeated tool friction | Tool/platform fix |
| High-pain unresolved pattern | Investigation backlog |

Start with:

```bash
aha incidents --limit 50 --json
aha incidents --state resolved --json
aha incidents --state unresolved --json
aha read <sample_ref> --before 3 --after 10 --md
```

See `docs/patterns-and-interventions.md` for the manual classifier and artifact templates.

## Compared with Claude History Explorer and QMD

| Tool | Search scope | Search engine | Retrieval style | Best fit |
|---|---|---|---|---|
| `aha` | Pi + Claude Code + Codex + OpenCode, across machines after ingest | SQLite FTS5 over a local corpus | Search returns refs; `read <ref>` expands to full context/artifact text | Private cross-agent archive and agent-friendly retrieval |
| Claude History Explorer | Claude Code history only | On-demand parsing/regex-style local exploration | Browse/search Claude sessions directly | Lightweight Claude-only exploration |
| QMD-style workflows | Usually document/session search with agent-oriented outputs | Depends on QMD setup | Treat snippets as leads, then retrieve cited context | Query/read discipline and citation-like workflows |

`aha` borrows the QMD lesson that snippets are leads, not evidence: agents should search, read the returned refs, then answer from retrieved context. It differs from Claude History Explorer by capturing immutable multi-source content-addressed snapshots and merging them into a reusable local corpus instead of searching one live Claude tree on demand.

## Core journeys

### First local archive

```bash
aha init --accept-secrets
aha refresh
```

`refresh` is the short path: push only new file versions from configured sources (an unchanged machine writes nothing to the depot), then pull snapshots the corpus has not seen — fetching only unknown blobs.

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

The daily surface is intentionally small:

- `aha refresh` — update the local corpus.
- `aha search` → `aha read` — find leads, then retrieve evidence.
- `aha incidents` — inspect recurring failures and observed fix paths.
- `aha serve` — browse Search / Failures / Sources locally.
- `aha mcp` — expose read-only tools to coding agents.

See `docs/command-inventory.md` for the human command inventory and `docs/commands.md` for generated usage strings, flags, examples, and JSON contracts.

## Supported sources

| Source | Default root | Format |
|---|---|---|
| Pi | `~/.pi/agent/sessions` | JSONL session files |
| Claude Code | `~/.claude/projects` | JSONL project/session files, including `agent-*` subagents |
| Codex | `~/.codex/sessions` | JSONL rollout/session files |
| OpenCode | `$XDG_DATA_HOME/opencode` when set, otherwise `~/.local/share/opencode` | SQLite database (`opencode.db`), converted to JSONL during discovery |

A source is read-only during snapshot. For JSONL sources, raw files are stored as content-addressed blobs listed in the snapshot manifest and preserved for provenance. OpenCode's SQLite database is converted to deterministic, lossless JSONL during discovery — the original `data` JSON of every `session`/`message`/`part` row is preserved verbatim, and the snapshot stores those JSONL files. The source database is copied (with any WAL/SHM sidecars that are present) into a private export cache before reading and is never written to. `$OPENCODE_DB` is an exclusive database-path override; when it is not set, release-channel databases (`opencode-*.db`) beside the default are picked up automatically. The export cache defaults under the user cache directory (`.../aha/opencode-export/<db-hash>/`), uses private directory/file modes, and can be redirected with `AHA_OPENCODE_EXPORT_DIR` or size-limited with `AHA_OPENCODE_MAX_DB_BYTES`.

### Verifying an adapter against a real machine

`scripts/smoketest.sh <opencode|codex|claude|pi> [SOURCE_ROOT]` runs a safe end-to-end check (discovery → snapshot → ingest → search → read) against your real history. Every artifact it generates goes under a single `/tmp` directory — a throwaway corpus, depot, config, cache/build cache, and (for OpenCode) the JSONL export — so your real `~/.aha`/`~/.config/aha` are untouched and there is nothing to clean up. It fingerprints and content-hashes source files before and after (plus `integrity_check` of OpenCode databases) and fails if anything changed, giving a strong read-only regression check.

```bash
scripts/smoketest.sh opencode          # uses the default root
scripts/smoketest.sh codex ~/.codex/sessions
```

The live R2 test is separately isolated: `scripts/r2-smoketest.sh` defaults to
the project's pinned dedicated test bucket/account, securely prompts for
missing `AHA_R2_SMOKETEST_*` credentials, and never falls back to production
`AHA_R2_*`, `R2_*`, or `AWS_*` providers. Use a distinct Object Read & Write
token scoped only to that test bucket; flags remain available to override the
pinned target with another dedicated test destination.

## Defaults

| Setting | Default |
|---|---|
| Config | `~/.config/aha/config.jsonc` |
| Corpus | `~/.aha` |
| Depot | `~/.aha/depot` local content-addressed snapshot store |
| Machine ID | sanitized local hostname |
| Tool output indexing | off |
| Redaction | `none-v1` (set `v1` to redact indexed projections at ingest) |

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
4. Prefer query-only commands (`search`, `read`, `incidents`, `status`, `conflicts`) unless the user explicitly asks to snapshot/ingest. `doctor` is diagnostic but may create/update the private OpenCode JSONL export cache while counting OpenCode sessions.
5. Check `aha status --json` for `redaction_levels`; if the corpus is `none-v1`, do not assume secrets are redacted.

## Accepted v1 limits

- Redaction is pattern-based and applies to derived corpus projections only; raw depot blobs remain unredacted.
- No Windows support until v2.
- `include_images=false` suppresses normalized image assets/blobs, but raw depot blobs/session JSON may still contain embedded image bytes.
- `read` shows file-order context, not source-native branch/thread reconstruction.
- Tool output is preserved in raw files but not indexed by default.
- Conflict UX can improve.

## Project docs

- `CHANGELOG.md` — notable unreleased changes.
- `docs/command-inventory.md` — human command inventory and common workflows.
- `docs/commands.md` — generated command metadata, examples, and JSON contracts.
- `docs/user-journeys.md` — journeys and defaults.
- `docs/trust.md` — privacy/trust model and verification.
- `docs/onboarding.md` — verifiable local-first onboarding, troubleshooting, and optional R2 setup.
- `docs/depot-lifecycle.md` — depot states, transitions, and how the default depot is chosen and switched.
- `docs/r2-bucket-settings.md` — recommended R2 bucket, token, endpoint, and audit settings.
- `docs/architecture.md` — high-level architecture diagram and flows.
- `docs/mcp-spec.md` — read-only stdio MCP server spec and tool surface.
- `docs/serve-ui-spec.md` — journey-oriented local dashboard spec for `aha serve`.
- `docs/redaction-spec.md` — implemented v1.1 corpus-projection redaction plus deferred v1.2+ designs.
- `docs/outcome-weighting-spec.md` — design for the incidents surface: ranking recurring failures by the fix that actually worked (resolution-path mining over `tool_invocations`).
- `docs/patterns-and-interventions.md` — manual guide for turning incidents into the right artifact: runbook, skill, dynamic workflow, tool/platform fix, or investigation backlog.
- `docs/research/agent-trace-tools.md` — neighbour-tool analysis (Tracebase, Self-Care, claude-session-analyzer, agenttrace, skill-optimizer, Crune, retrospective-skill, claude-history, plus broader survey).
- `docs/research/openinference.md` — OpenInference semantic-convention reference.
- `docs/research/openinference-impact-estimate.md` — data-size and performance estimate for adopting OpenInference's schema.
- `docs/depot-v2-spec.md` — content-addressed snapshot depot (blobs + manifests): the current depot design.
- `docs/agent-history-aggregator-spec.md` — full v1 spec (historical; the depot portions are superseded by `docs/depot-v2-spec.md`).
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
