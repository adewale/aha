# Trust and Verification

`aha` reads private agent histories. Those histories can include proprietary code, credentials accidentally pasted into prompts, filesystem paths, images, tool output, and personal working notes. Treat bundles and corpora as private.

This document states the v1 trust model and how to verify it.

## Guarantee 1: source histories are not mutated

`aha snapshot` reads Pi and Claude Code session roots and writes a separate bundle. It must not modify, delete, or rewrite source session files.

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
go test ./internal/corpus -run Immutable
```

## Guarantee 3: local-only v1, no network behavior

V1 does not upload bundles or corpora. There is no hosted service and no share command.

How this is enforced:

- application packages do not import Go network client/server packages;
- CI runs a static no-network-import check.

Verify locally:

```bash
go test ./internal/cli -run NoNetwork
```

## Guarantee 4: no v1 redaction

This is a warning, not a guarantee: v1 deliberately does **not** redact secrets. It preserves raw files so prompts can be reconstructed and future parsers can re-normalize data.

Implication:

- do not publish bundles or corpora;
- do not upload them to issue trackers;
- review before sharing logs or command output.

## What `aha` writes

Depending on command/configuration, `aha` may write:

- bundle files under the selected output directory;
- receipt JSON next to bundles;
- corpus SQLite/blob files under the corpus directory;
- config JSONC via `aha init`.

It should not write inside Pi or Claude Code source-history roots.

## Quick verification checklist

```bash
# Build/test core behavior
go test ./...
go vet ./...
go test -race ./...

# Static trust checks
go test ./internal/cli -run 'NoNetwork|Readme'
go test ./internal/adapters -run ReadOnly

# End-to-end private smoke test; use temp dirs and delete them after inspection
go build -o /tmp/aha ./cmd/aha
/tmp/aha snapshot --machine local-test \
  --source pi=$HOME/.pi/agent/sessions \
  --source claude-code=$HOME/.claude/projects \
  --out /tmp/aha-bundles \
  --accept-secrets
```
