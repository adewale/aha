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
aha snapshot
aha ingest
aha search "query"
```

Rationale:

- `init` makes defaults visible before data is copied.
- `--accept-secrets` records that v1 preserves raw private data.
- `snapshot` should not require paths for the standard Pi/Claude locations.
- `ingest` should know where snapshots were written.

Defaults used:

- machine ID: sanitized local hostname, written to config;
- sources: `~/.pi/agent/sessions` and `~/.claude/projects`;
- bundle output: `~/agent-session-bundles`;
- corpus: `~/.aha`;
- subagents/images: included;
- redaction: none in v1.

## Journey 2: routine refresh

User goal: “Add the sessions created since my last snapshot.”

```bash
aha snapshot
aha ingest
```

Rationale:

- Refresh should be muscle memory.
- `snapshot` creates a new dated bundle.
- `ingest` with no arguments reads configured bundles and skips duplicates.

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

## Journey 4: import another machine

User goal: “Merge a bundle copied from my work laptop.”

```bash
aha ingest ~/Downloads/aha-sessions-work-mac.tar.zst
aha search "query" --machine work-mac
```

Rationale:

- Bundle identity and machine provenance come from the manifest.
- Users should not need to rename files or edit config before import.
- Duplicate imports should be harmless.

## Journey 5: automation

User goal: “Run this in a script with temporary paths.”

```bash
AHA_ACCEPT_SECRETS=1 aha snapshot \
  --machine ci-mac \
  --source pi="$HOME/.pi/agent/sessions" \
  --source claude-code="$HOME/.claude/projects" \
  --out "$RUNNER_TEMP/aha-bundles"

aha ingest --corpus "$RUNNER_TEMP/aha-corpus" "$RUNNER_TEMP/aha-bundles"/*.tar.zst
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
| `aha snapshot` | Use config/default machine ID, sources, and bundle output. Require prior acknowledgement via config, `--accept-secrets`, or `AHA_ACCEPT_SECRETS=1`. |
| `aha ingest` | Ingest all `*.tar.zst` from configured bundle output into configured corpus. |
| `aha search <query>` | Search configured corpus, limit 20. |
| `aha read ...` | Read from configured corpus. |
| `aha status` / `aha conflicts` | Inspect configured corpus. |

## When to use flags

Use flags for:

- deterministic test bundles (`--captured-at`, `--bundle-id`);
- copied bundles from another machine;
- multiple corpora;
- nonstandard Pi or Claude roots;
- temporary workspaces and CI.
