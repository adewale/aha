# User Journeys and Defaults

`aha` is optimized for a local-first archive workflow: snapshot private histories, ingest immutable bundles, then search the corpus. The defaults come from the journeys below.

Design rules:

- the common path should be short;
- privacy acknowledgement should be explicit once, not repeated forever;
- default paths should be visible in config;
- copied bundles should work without editing config;
- scripts should still be able to override everything.

## Journey 1: first local archive

User goal: “I want all of this machine's Pi and Claude Code history searchable.”

```bash
aha init --accept-secrets
aha refresh
aha search "query"
```

Rationale:

- `init` makes defaults visible before data is copied.
- `--accept-secrets` records that v1 preserves raw private data.
- `refresh` creates a snapshot bundle and ingests that bundle into the local corpus.
- The user should not need to remember Pi/Claude paths for the standard layout.

Defaults used:

- machine ID: sanitized local hostname, written to config;
- sources: `~/.pi/agent/sessions` and `~/.claude/projects`;
- bundle output: `~/agent-session-bundles`;
- corpus: `~/.aha`;
- subagents/images: included;
- redaction: none in v1.

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
- Default indexing favors human conversation, summaries, and text artifacts; raw tool output is preserved but not indexed.

## Journey 4: create a repo and ingest bundles

User goal: “Create a separate aggregation repo and put bundles into it.”

```bash
aha ingest --repo ~/aha-work ~/Downloads/aha-sessions-work-mac.tar.zst
aha search --repo ~/aha-work "query" --machine work-mac
```

Rationale:

- Ingest creates the repo/corpus directory if needed.
- Bundle identity and machine provenance come from the manifest.
- Users should not need to rename files or edit config before import.
- Duplicate imports should be harmless.

## Journey 5: automation

User goal: “Run this in a script with temporary paths.”

```bash
AHA_ACCEPT_SECRETS=1 aha refresh \
  --machine ci-mac \
  --source pi="$HOME/.pi/agent/sessions" \
  --source claude-code="$HOME/.claude/projects" \
  --out "$RUNNER_TEMP/aha-bundles" \
  --repo "$RUNNER_TEMP/aha-corpus" \
  --max-sessions 10
```

Rationale:

- Automation should not require editing user config.
- Explicit flags should override every default.
- Privacy acknowledgement can come from `AHA_ACCEPT_SECRETS=1` in controlled scripts.

## Defaults chosen from the journeys

| Command | No-flag behavior |
|---|---|
| `aha init` | Write JSONC config with hostname-derived `machine_id`, default roots, default corpus/bundle dirs, and privacy acknowledgement set to false. |
| `aha init --accept-secrets` | Same as `init`, but records one-time privacy acknowledgement. |
| `aha refresh` | Snapshot configured sources into configured bundle output, then ingest the new bundle into configured corpus. Supports `--session` and `--max-sessions` for 1-to-all local-session scope. |
| `aha snapshot` | Use config/default machine ID, sources, and bundle output. Supports `--session` and `--max-sessions`; requires prior privacy acknowledgement. |
| `aha ingest` | Ingest explicit bundle paths, or all `*.tar.zst` from configured bundle output when no paths are given. `--repo` is an alias for the corpus directory. |
| `aha search <query>` | Search configured corpus, limit 20. |
| `aha read ...` | Read from configured corpus. |
| `aha status` / `aha conflicts` | Inspect configured corpus. |

## Why each command exists

| Command | Reason |
|---|---|
| `refresh` | Common local operation: make the aggregation corpus current in one step, scoped from one to all local sessions. |
| `snapshot` | Capture immutable evidence without changing the corpus; useful for backup, copy, deterministic testing, and scoped local-session bundles. |
| `ingest` | Create/open a repo and merge copied/existing bundles; useful for multi-machine history and parser/schema reruns. |
| `search` | Primary retrieval action. |
| `read` | Context expansion around compact search results. |
| `status` | Explain corpus health and counts. |
| `conflicts` | Surface quarantined merge conflicts without cluttering normal search. |
| `doctor` | Debug source discovery, config, and adapter availability. |
| `init` | Optional: materialize editable defaults and one-time privacy acknowledgement. |

## When to use flags

Use flags for:

- deterministic test bundles (`--captured-at`, `--bundle-id`);
- copied bundles from another machine;
- multiple corpora;
- nonstandard Pi or Claude roots;
- temporary workspaces and CI.
