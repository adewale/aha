# Recommended User Journeys

These journeys define the defaults `aha` optimizes for. Flags remain available for automation, deterministic tests, and unusual layouts, but normal local use should be short.

## Journey 1: First local setup

Goal: create a private local corpus for the current machine.

```bash
aha init --accept-secrets
aha snapshot
aha ingest
aha search "query"
```

Defaults used:

- machine ID: sanitized local hostname, written visibly into config;
- sources: existing default Pi and Claude Code roots;
- bundle output: `~/agent-session-bundles`;
- corpus: `~/.aha`;
- subagents/images: included;
- redaction: none in v1, acknowledged once in config.

## Journey 2: Routine refresh on one machine

Goal: add recent sessions to the local corpus.

```bash
aha snapshot
aha ingest
```

`ingest` with no bundle arguments reads `*.tar.zst` from the configured bundle output directory. Duplicate bundles are skipped idempotently.

## Journey 3: Search and read

Goal: find a prior conversation and inspect context.

```bash
aha search "dynamic workflows"
aha read --session <session-key> --entry <entry-id> --before 3 --after 5
```

Defaults used:

- corpus: `~/.aha`;
- search limit: 20;
- default index excludes tool output but includes user/assistant text, summaries, and text artifacts.

## Journey 4: Import another machine's bundle

Goal: merge a bundle copied from another machine.

```bash
aha ingest ~/Downloads/aha-sessions-other-machine.tar.zst
aha search "query" --machine other-machine
```

Defaults used:

- corpus: `~/.aha`;
- bundle identity and machine provenance come from the bundle manifest, not the filename.

## Journey 5: Automation without editing config

Goal: run in scripts or CI with explicit paths.

```bash
AHA_ACCEPT_SECRETS=1 aha snapshot \
  --machine ci-mac \
  --source pi="$HOME/.pi/agent/sessions" \
  --source claude-code="$HOME/.claude/projects" \
  --out "$RUNNER_TEMP/aha-bundles"

aha ingest --corpus "$RUNNER_TEMP/aha-corpus" "$RUNNER_TEMP/aha-bundles"/*.tar.zst
```

Defaults deliberately overridden:

- machine ID;
- source roots;
- bundle output;
- corpus path;
- privacy acknowledgement via environment variable.

## Defaults chosen from these journeys

| Command | No-flag behavior |
|---|---|
| `aha init` | Write JSONC config with hostname-derived `machine_id`, default roots, default corpus/bundle dirs, and privacy acknowledgement set to false. |
| `aha init --accept-secrets` | Same as `init`, but records one-time privacy acknowledgement. |
| `aha snapshot` | Use config/default machine ID, default sources, and default bundle output. Require `--accept-secrets`, `AHA_ACCEPT_SECRETS=1`, or config acknowledgement. |
| `aha ingest` | Ingest all `*.tar.zst` from configured bundle output into configured corpus. |
| `aha search <query>` | Search configured corpus. |
| `aha read ...` | Read from configured corpus. |
| `aha status` / `aha conflicts` | Inspect configured corpus. |

## Non-default cases

Use flags when:

- creating deterministic test bundles (`--captured-at`, `--bundle-id`);
- ingesting a copied bundle from another machine;
- keeping multiple corpora;
- using nonstandard Pi or Claude roots;
- scripting around a temporary workspace.
