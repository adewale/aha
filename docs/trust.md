# Trust and Verification

`aha` reads private agent histories. Those histories can include proprietary code, credentials accidentally pasted into prompts, filesystem paths, images, tool output, and personal working notes. Treat bundles and corpora as private.

This document states the trust model and how to verify it. By default `aha` is local-only. Remote depot support, when configured with `r2:...`, is explicit opt-in and changes the upload/privacy posture.

## Guarantee 1: source histories are not mutated

`aha snapshot` reads Pi, Claude Code, and Codex session roots and writes a separate bundle. It must not modify, delete, or rewrite source session files.

How this is enforced:

- source-specific adapters are read-only by design;
- tests verify snapshot read-only behavior with source hashes/mtimes;
- static tests reject write/delete/rename calls inside source adapters.

Verify locally:

```bash
go test ./internal/archive ./internal/adapters
```

## Guarantee 2: ingest uses immutable bundles

`aha ingest` reads a `.tar.zst` bundle and writes to the corpus. It must not reread mutable live source paths to decide parsed identity.

How this is enforced:

- ingest stages and validates the bundle before promotion;
- Pi session identity is parsed from bundled bytes, not live `raw_path`;
- regression tests mutate the live Pi file after snapshot and verify ingest still uses the bundled session ID.

Verify locally:

```bash
go test ./internal/corpus -run IngestPiIdentity
```

## Guarantee 3: local-only by default

By default, `aha` uses a local depot at `~/.aha/depot` and a local corpus at `~/.aha`. It does not upload bundles or corpora unless you explicitly configure or pass an R2 depot such as `--depot r2:aha-depot`.

How this is enforced:

- only `internal/depot` may import Go network client/server packages;
- all other command, archive, adapter, corpus, search, and read packages remain network-free;
- CI runs a static no-network-import check for the core.

Verify locally:

```bash
go test ./internal/cli -run NoNetworkImports
```

## Guarantee 4: remote depot is opt-in and private

If you use an R2 depot, unredacted bundles leave your machine and are stored in your private bucket. `aha` treats that bucket as private storage for sensitive history. Recommended bucket/token settings are documented in `docs/r2-bucket-settings.md`.

How this is enforced:

- R2 credentials are loaded from environment variables (future keychain/0600-file support may be added), not manifests or bundles;
- R2 credentials must not appear in catalog shards, command JSON, config, or logs;
- downloaded depot bundles are still validated by bundle SHA, manifest file list, entry sizes, and per-file SHA before ingest.

## Guarantee 5: no v1 redaction

This is a warning, not a guarantee: v1 deliberately does **not** redact secrets. It preserves raw files so prompts can be reconstructed and future parsers can re-normalize data.

Implication:

- do not publish bundles or corpora;
- do not upload them to issue trackers;
- review before sharing logs or command output.

## What `aha` writes

Depending on command/configuration, `aha` may write:

- bundle files under the selected local depot directory;
- corpus SQLite/blob files under the corpus directory;
- config JSONC via `aha init`.

It should not write inside Pi, Claude Code, or Codex source-history roots.

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
  --depot local:/tmp/aha-bundles
```
