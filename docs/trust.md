# Trust and Verification

`aha` reads private agent histories. Those histories can include proprietary code, credentials accidentally pasted into prompts, filesystem paths, images, tool output, and personal working notes. Treat depot contents and corpora as private.

This document states the trust model and how to verify it. By default `aha` is local-only. Remote depot support, when configured with `r2:...`, is explicit opt-in and changes the upload/privacy posture.

## Guarantee 1: source histories are not mutated

`aha snapshot` reads Pi, Claude Code, Codex, and OpenCode session roots and writes separate content-addressed blobs plus a snapshot manifest into the depot. It must not modify, delete, or rewrite source session files or OpenCode databases.

How this is enforced:

- JSONL source adapters are read-only by design;
- OpenCode discovery reads a private copied/exported database snapshot and never opens the source database for writing;
- tests verify snapshot/read-only behavior with source hashes/mtimes and OpenCode DB content hashes;
- static tests reject write/delete/rename calls inside source adapters; OpenCode's generated JSONL cache is isolated in `internal/opencodeexport` and documented below.

Verify locally:

```bash
go test ./internal/archive ./internal/adapters ./internal/opencodeexport
```

## Guarantee 2: ingest uses immutable depot content

`aha ingest` reads write-once depot blobs and manifests (or, with an explicit path, imports a portable v1-format `.tar.zst` bundle file) and writes to the corpus. It must not reread mutable live source paths to decide parsed identity.

How this is enforced:

- pulled blobs are verified against their content-address SHA-256, and imported bundle files are staged and validated, before promotion;
- Pi session identity is parsed from captured bytes, not live `raw_path`;
- regression tests mutate the live Pi file after snapshot and verify ingest still uses the captured session ID.

Verify locally:

```bash
go test ./internal/corpus -run IngestPiIdentity
```

## Guarantee 3: local-only by default

By default, `aha` uses a local depot at `~/.aha/depot` and a local corpus at `~/.aha`. It does not upload snapshots or corpora unless you explicitly configure or pass an R2 depot such as `--depot r2:aha-depot`.

How this is enforced:

- network imports are permitted only in `internal/depot` (outbound R2/S3 for the remote depot), `internal/server` (the read-only loopback dashboard served by `aha serve`), and the CLI wrapper `internal/cli/command_serve.go`;
- all other command, archive, adapter, corpus, search, and read packages remain network-free;
- the dashboard binds to `127.0.0.1` by default and refuses non-loopback addresses unless `--allow-remote` (or `AHA_ALLOW_REMOTE=1`) is set, every route is read-only, and the server never initiates outbound traffic;
- CI runs a static no-network-import check for the core that allowlists the paths above.

Verify locally:

```bash
go test ./internal/cli -run NoNetworkImports
```

## Guarantee 4: remote depot is opt-in and private

If you use an R2 depot, unredacted history blobs leave your machine and are stored in your private bucket. `aha` treats that bucket as private storage for sensitive history. Recommended bucket/token settings are documented in `docs/r2-bucket-settings.md`.

How this is enforced:

- R2 credentials are loaded from environment variables (future keychain/0600-file support may be added), not manifests or blobs;
- R2 credentials must not appear in manifests, pointers, the machines index, command JSON, config, or logs;
- downloaded blobs are still verified against their content-address SHA-256, and manifests against their own SHA-256 identity, before ingest.

## Guarantee 5: projection redaction is explicit and observable

By default, `redaction` is `none-v1` for backwards compatibility. When configured as `v1`, ingest redacts known secret patterns from derived corpus projections before they reach `messages`, `tool_invocations`, `entries.raw_json`, artifact text, or FTS. Raw depot blobs remain unredacted provenance.

How this is enforced:

- CLI `ingest` and `refresh` both construct their ingestor from config, including extra redaction patterns;
- sessions are stamped with `sessions.redaction_level` only when first inserted, so existing raw projections are not silently mislabeled;
- per-entry counts are append-only in `redactions`; session/artifact redaction events are recorded in `redaction_events`;
- `aha status --json`, `aha verify --json`, and `aha doctor --json` surface redaction counts/levels.

Implication:

- do not publish raw depot contents;
- check `redaction_levels` before assuming a corpus is redacted;
- review command output before sharing logs or issue attachments.

## What `aha` writes

Depending on command/configuration, `aha` may write:

- blob/manifest/pointer objects under the selected local depot directory;
- corpus SQLite/blob files under the corpus directory;
- config JSONC via `aha init`;
- a private OpenCode JSONL export cache during OpenCode `Discover` (including `doctor`, `snapshot`, and `refresh`): by default under the user cache directory at `aha/opencode-export/<db-hash>/`, or under `AHA_OPENCODE_EXPORT_DIR` when set; directories are forced to `0700` and JSONL/lock files to `0600`; stale JSONL files are pruned after each serialized export;
- optional local pprof files when `--cpuprofile`, `--memprofile`, `AHA_CPU_PROFILE`, or `AHA_MEM_PROFILE` is explicitly set.

It should not write inside Pi, Claude Code, Codex, or OpenCode source-history roots. Treat the OpenCode export cache, profiles, depots, and corpora as local private artifacts; do not attach them to public issues without review because they can reveal prompts, source snippets, filesystem paths, and workload shape.

## Quick verification checklist

```bash
# Build/test core behavior
go test ./...
go vet ./...
go test -race ./...

# Static trust checks
go test ./internal/cli -run 'NoNetworkImports|Readme'
go test ./internal/adapters -run SourceAdaptersStayReadOnly

# Corpus invariant/repair tooling
go test ./internal/corpus -run 'Verify|ReconcileFTS'

# End-to-end private smoke test; use temp dirs and delete them after inspection
go build -o /tmp/aha ./cmd/aha
AHA_ACCEPT_SECRETS=1 /tmp/aha snapshot \
  --machine local-test \
  --source pi=$HOME/.pi/agent/sessions \
  --source claude-code=$HOME/.claude/projects \
  --depot local:/tmp/aha-depot
```
