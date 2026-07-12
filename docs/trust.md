# Trust and privacy model

Aha reads private coding-agent histories and therefore treats every Archive and Workspace as sensitive.

## Source ownership

Agent histories are owned by Pi, Claude Code, Codex, and OpenCode. Aha may discover, open, and stably copy configured files; it must never modify, rename, delete, lock, migrate, or rewrite them.

Archive and Workspace destinations are rejected when they contain or overlap an enabled source root, including symlink-resolved overlap.

## Upload boundary

`archive upload`:

- validates configuration and the Archive marker before capture/publication;
- reads only configured agent-history roots;
- uploads only unknown content-addressed blobs;
- publishes immutable manifests;
- advances one machine pointer using conditional writes;
- never opens, locks, creates, or changes a Workspace;
- never auto-initialises an Archive.

The capture cache is advisory and stored outside the Workspace. Failure to persist it affects performance only.

## Download boundary

`archive download`:

- validates marker, index, every latest pointer, and the destination before mutation;
- freezes an opaque download plan;
- rejects unrelated non-empty directories and Archive mismatches;
- fetches only unknown blobs;
- records the materialised vector only after the complete plan succeeds;
- never reads mutable agent histories;
- never writes the Archive.

A failed preflight creates no Workspace directory, SQLite database, WAL, lifecycle lock, config change, Archive write, or publication.

## Local defaults

```text
Archive:   local:~/.aha/archive
Workspace: ~/.aha/workspace
```

Nothing is sent over the network unless an R2 Archive is explicitly configured or addressed.

## R2 credentials

Aha reads R2 credentials from environment variables only:

```text
AHA_R2_ACCOUNT_ID
AHA_R2_ACCESS_KEY_ID
AHA_R2_SECRET_ACCESS_KEY
AHA_R2_ENDPOINT       (optional)
AHA_R2_REGION         (optional)
```

Only non-secret account/endpoint settings may be persisted. Credential values never appear in config, status JSON, diagnostics, errors, progress, or test fixtures.

R2 bucket names require an explicit `r2:` address at CLI boundaries. Bare bucket-looking strings are not reinterpreted as local paths.

## Network boundary

Production network imports are restricted to:

- `internal/depot`: outbound R2/S3 Archive access;
- `internal/server`: the read-only dashboard HTTP server;
- `internal/cli/command_serve.go`: dashboard listener wiring.

Archive upload/download code receives validated capabilities rather than raw network destinations.

## Dashboard

`aha dashboard` binds to loopback by default, enforces Host-header checks, rejects non-JSON POST requests, limits body sizes, sets a content-security policy, and exposes read-only operations.

Non-loopback binding requires explicit `--allow-remote` and a bearer token. Token comparisons are constant-time and only the first `Authorization` header is honoured.

## MCP

`aha mcp serve` exposes a read-only stdio server. `aha mcp check` opens the Workspace and registers tools without serving. Protocol stdout is isolated from diagnostics on stderr. MCP application/tool failures are normalised by aha; SDK-owned protocol failures are not normalised by aha.

## Redaction

Redaction is a Workspace projection policy. It does not alter Archive bytes or source histories. The configured level is stamped on ingested sessions, and audit counts are visible through Workspace/system status.

Because the Archive preserves raw history, redaction cannot make an Archive safe to publish. Treat both resources as private even when Workspace text is redacted.

## Error boundary

Every CLI failure presents:

- one stable error code;
- one concise, state-aware message;
- exactly one next action;
- optional allowlisted diagnostics only under `--verbose-errors`.

Raw dependency, SQL, filesystem, endpoint, and credential-bearing errors remain internal. JSON failures use `aha.error.v1` on stderr.

## Progress boundary

Progress is ordered and written only to stderr. JSON stdout remains one final document. Progress reports observable phases, counters, totals, units, and elapsed time; it never fabricates an ETA.

## Recovery

Archive content is immutable and retained. Workspace repair builds a verified sibling, syncs it, atomically exchanges directories, and preserves the former Workspace as a backup. There is no unsafe no-backup mode.

## Verification

```bash
aha archive verify --deep
aha workspace verify
aha status --json
```

The pinned live R2 smoke suite uses source-pinned test capabilities and a dedicated attested destination. Production credentials and destinations are forbidden in smoke paths.
