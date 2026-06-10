# User Journeys and Defaults

`aha` is optimized for a local-first archive workflow: push private histories
to a content-addressed depot as immutable snapshots, pull them into a local
corpus, then search it. The defaults come from the journeys below.

Design rules:

- the common path should be short;
- privacy acknowledgement should be explicit once, not repeated forever;
- default paths should be visible in config;
- copied bundles should work without editing config;
- scripts should still be able to override everything.

## Journey 1: first local archive

User goal: “I want all of this machine's Pi, Claude Code, Codex, and OpenCode history searchable.”

```bash
aha init --accept-secrets
aha refresh
aha search "query"
```

Rationale:

- `init` makes defaults visible before data is copied.
- `--accept-secrets` records that v1 preserves raw private data.
- `refresh` pushes this machine's state to the depot (unchanged state is recognized from the depot pointer alone; only new file versions upload) and pulls every machine's latest snapshot into the local corpus, fetching only unknown content.
- The user should not need to remember Pi/Claude/Codex paths for the standard layout.

Defaults used:

- machine ID: sanitized local hostname, written to config;
- sources: `~/.pi/agent/sessions`, `~/.claude/projects`, and `~/.codex/sessions`;
- depot: local bundle store at `~/.aha/depot`; remote/R2 depot is opt-in;
- corpus: `~/.aha`;
- subagents/images: included;
- redaction: `none-v1` by default; set `v1` to redact corpus projections at ingest.

## Journey 2: routine refresh from one to all local sessions

User goal: “Add one session, a few sessions, or all local sessions to my local repo.”

```bash
# all configured local sessions
aha refresh

# the newest discovered local session
aha refresh --max-sessions 1

# specific session(s) by ID, filename, relative path, or path substring
aha refresh --session pi-session --session agent-worker
```

Rationale:

- Refresh should be muscle memory for all sessions.
- Scoping should not require a separate command.
- `--max-sessions N` covers quick tests and newest-N refreshes.
- `--session MATCH` covers exact or targeted local-session refreshes.
- The lower-level commands remain available when users want capture and ingest separated.

## Journey 3: search and read

User goal: “Find the conversation where we solved this before.”

```bash
aha search "dynamic workflows"
aha read --session <session-key> --entry <entry-id> --before 3 --after 5
```

Rationale:

- Search should default to the local corpus.
- Results must be readable without knowing the source agent's file format.
- Default indexing favors human conversation, summaries, and text artifacts; raw tool output is preserved but not indexed. Incidents store normalized command/error signatures for ranking, not raw tool output samples.

## Journey 4: hand someone one file (export/import)

User goal: “Give a colleague (or another machine without depot access) my history as a single file.”

```bash
aha export --machine work-mac --out work-mac.tar.zst
aha ingest --repo ~/aha-work work-mac.tar.zst
aha search --repo ~/aha-work "query" --machine work-mac
```

Rationale:

- `export` materializes a machine's latest depot snapshot as one portable v1 `bundle.tar.zst`; `ingest <file>` imports it. These two commands are the only place the v1 bundle format survives.
- Snapshot identity and machine provenance come from the manifest.
- Ingest creates the repo/corpus directory if needed.
- Duplicate imports are harmless (identity is the manifest hash).

## Journey 4½: bootstrap a new machine

User goal: “New laptop; give me my whole cross-machine history.”

```bash
aha init --accept-secrets
aha refresh
```

Rationale:

- The pull half of `refresh` discovers every machine in the depot (one index read), fetches each latest manifest, and downloads only blobs the fresh corpus lacks — each file version is parsed exactly once.
- No restore command is needed: the corpus is a rebuildable index over the depot by construction.

## Journey 4¾: contribute-only machines

User goal: “This box should publish its history and never download anyone else's.”

```bash
aha snapshot
```

Rationale:

- `snapshot` is push-only: it reads its own latest manifest as the diff baseline (a few KB), uploads only new file versions, and never touches another machine's namespace — guaranteed by the depot's per-machine write handles, not by convention.
- No corpus is required on the machine.

## Journey 5: automation

User goal: “Run this in a script with temporary paths.”

```bash
AHA_ACCEPT_SECRETS=1 aha refresh \
  --machine ci-mac \
  --source pi="$HOME/.pi/agent/sessions" \
  --source claude-code="$HOME/.claude/projects" \
  --depot "local:$RUNNER_TEMP/aha-bundles" \
  --repo "$RUNNER_TEMP/aha-corpus" \
  --max-sessions 10
```

Rationale:

- Automation should not require editing user config.
- Explicit flags should override every default.
- Privacy acknowledgement can come from `AHA_ACCEPT_SECRETS=1` in controlled scripts.

## Journey 6: an agent queries history as tools

User goal: “Let my coding agent search and read prior sessions without shelling out per call.”

```bash
# Register once in the MCP host's config:
#   { "mcpServers": { "aha": { "command": "aha", "args": ["mcp"] } } }
aha mcp

# Smoke-test the wiring before connecting a host:
aha mcp --dry-run
```

Rationale:

- Agents already speak MCP; `aha mcp` exposes the read tools (`search`, `read`, `incidents`, `incident_trajectory`, `overview`, `status`, `verify`, `conflicts`, `corpus_size`, `doctor`) over stdio JSON-RPC.
- It reuses the same corpus/search code as the CLI, so results match `--json` output exactly.
- It is read-only by construction: snapshot/refresh/ingest are not reachable, so an agent cannot mutate the corpus.
- `--dry-run` opens the corpus, registers tools, prints a one-line summary, and exits — confirms a host config before stdio carries protocol traffic.
- For code-mode runtimes, the typed surface in `clients/typescript/` lets one program fan out (`search` → filter → parallel `read`) over a long-lived transport. It is still one MCP call per `read`; see `docs/mcp-spec.md`.

## Journey 7: browse the corpus locally

User goal: “Give me a quick local UI to search, read, and see conflicts.”

```bash
aha serve
# → aha dashboard listening on http://127.0.0.1:18428
```

Rationale:

- Same read tools as `mcp`, served as HTTP/JSON plus a tiny embedded UI; no Node runtime on the host.
- Loopback-only and read-only by default; the `Host` header is validated to blunt DNS-rebinding, and non-loopback binds require `--allow-remote`.
- Result clicks deep-link the ref into the URL fragment so a view is reloadable and shareable.

## Journey 8: turn recurring incidents into intervention artifacts

User goal: “What should I create so this failure pattern costs less next time?”

```bash
aha incidents --limit 50 --json
aha incidents --state resolved --limit 25 --json
aha incidents --state unresolved --limit 25 --json
aha read <sample-ref> --before 3 --after 10 --md
```

Rationale:

- Incidents are evidence, not automatic prescriptions.
- Resolved/partial incidents can become runbooks, skills, dynamic workflows, or tool/platform fixes depending on the pattern shape.
- Unresolved high-pain incidents usually become investigation backlog items until a reliable fix path exists.
- `docs/patterns-and-interventions.md` provides the manual classifier and artifact templates.

## Defaults chosen from the journeys

| Command | No-flag behavior |
|---|---|
| `aha init` | Write JSONC config with hostname-derived `machine_id`, default roots, default local depot/corpus dirs, and privacy acknowledgement set to false. |
| `aha init --accept-secrets` | Same as `init`, but records one-time privacy acknowledgement. |
| `aha refresh` | Push state to the configured depot (unchanged state reuses the parent snapshot; only new file versions upload), then pull every machine's latest snapshot into the configured corpus. Supports `--session`/`--max-sessions` scoping and `--force` to bypass the capture cache. |
| `aha snapshot` | Push-only: capture and publish to the configured depot without a corpus. Supports `--session`, `--max-sessions`, `--force`; requires prior privacy acknowledgement. |
| `aha ingest` | Pull every machine's latest depot snapshot when no paths are given, or import explicit v1 bundle files. `--repo` is an alias for the corpus directory. |
| `aha search <query>` | Search configured corpus, limit 20. |
| `aha read ...` | Read from configured corpus. |
| `aha status` / `aha verify` / `aha conflicts` | Inspect configured corpus; `verify --repair-fts` repairs derived FTS rows. |

## Why each command exists

| Command | Reason |
|---|---|
| `refresh` | Common local operation: make the depot and corpus current in one step, scoped from one to all local sessions. |
| `snapshot` | Push immutable evidence to the depot without touching a corpus; the contribute-only machine's whole interface. |
| `ingest` | Create/open a repo and pull depot snapshots (or import exported bundle files); useful for multi-machine history and parser/schema reruns. |
| `export` | Materialize a machine's latest snapshot as one portable file for hand-off. |
| `search` | Primary retrieval action. |
| `read` | Context expansion around compact search results. |
| `status` | Explain corpus health and counts. |
| `verify` | Check corpus invariants and optionally repair derived FTS rows. |
| `conflicts` | Surface quarantined merge conflicts without cluttering normal search. |
| `depot` | Initialize, list machine snapshots, and verify the durable content-addressed store. |
| `doctor` | Debug source discovery, config, corpus, depot, and adapter availability. |
| `mcp` | Expose read tools to coding agents over stdio MCP without per-call subprocesses. |
| `serve` | Read-only local dashboard over the same tool surface for human browsing. |
| `init` | Optional: materialize editable defaults and one-time privacy acknowledgement. |

## When to use flags

Use flags for:

- deterministic test snapshots (`--captured-at`);
- exported bundles from another machine;
- multiple corpora;
- nonstandard Pi, Claude Code, Codex, or OpenCode roots;
- temporary workspaces and CI.
