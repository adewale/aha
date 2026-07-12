# aha

Aha aggregates coding-agent histories into a private, durable Archive and materialises a local Workspace for search, evidence inspection, failure analysis, MCP, and a local dashboard.

```text
Agent histories --archive upload--> Archive --archive download--> Workspace
```

Aha is local-first. The default Archive and Workspace are local; Cloudflare R2 is an explicit, private Archive option. Aha never writes agent-owned history files and never persists R2 credentials.

## Install from source

```bash
go install ./cmd/aha
aha version --json
```

Development builds report their product version, commit identity, build time, and dirty state so an old installed binary cannot masquerade as the current checkout.

## First use

```bash
aha init --acknowledge-raw-history
aha archive upload
aha archive download
aha search 'needle'
aha show '<ref returned by search>'
```

`init` writes config, assigns a stable machine ID, initialises the default local Archive, and validates the default Workspace destination. The Workspace database is created by the first download.

## The model

### Agent histories

Mutable source files and databases owned by Pi, Claude Code, Codex, and OpenCode. Aha reads them but never manages or modifies them.

### Archive

Durable aggregated truth across machines. An Archive is a local directory or an R2 bucket. Content-addressed blobs are write-once; each machine publishes immutable manifests and advances only its own latest pointer.

```bash
aha archive init [ARCHIVE]
aha archive set-default ARCHIVE
aha archive status [ARCHIVE]
aha archive upload [ARCHIVE]
aha archive download [ARCHIVE] --workspace PATH
aha archive verify [ARCHIVE] --deep
```

Upload never opens or changes a Workspace. Download never reads agent histories or writes the Archive. There is deliberately no `sync` command: compose the two explicit transitions only when that is your intent.

### Workspace

A local, rebuildable materialisation of one Archive's selected latest snapshots plus SQLite/FTS processing state. A Workspace is durably bound to one Archive identity and cannot silently download from another.

```bash
aha workspace set-default PATH
aha workspace status [PATH]
aha workspace verify [PATH] [--repair-fts]
aha workspace repair [PATH] --backup
aha workspace conflicts [PATH]
```

Repair builds and verifies a sibling replacement, durably syncs it, atomically exchanges it, and preserves the former Workspace as a backup. Unsafe no-backup repair is not available.

## Inspection and processing

```bash
aha status --json
aha search [--workspace PATH] QUERY
aha show [--workspace PATH] REF
aha analyse failures [--workspace PATH]
aha dashboard [--workspace PATH]
aha mcp check [--workspace PATH]
aha mcp serve [--workspace PATH]
```

- `status` reports independent upload and download gaps and exactly one next action.
- `search` returns stable, copyable references.
- `show` retrieves contextual evidence for a reference.
- `analyse failures` ranks recurring tool-call failures and observed resolution paths; it does not generate or install skills.
- `dashboard` serves a read-only trace browser on loopback by default.
- `mcp check` validates the local MCP surface without serving; `mcp serve` runs the read-only stdio server.

## Configuration

Default path: `~/.config/aha/config.jsonc` (or `$XDG_CONFIG_HOME/aha/config.jsonc`).

```jsonc
{
  "machine_id": "work-mac",
  "sources": [
    {"type": "pi", "root": "~/.pi/agent/sessions", "enabled": true}
  ],
  "archive": {"type": "local", "location": "~/.aha/archive"},
  "workspace_dir": "~/.aha/workspace",
  "path_mode": "raw",
  "include_subagents": true,
  "include_images": true,
  "index_tool_output": false,
  "redaction": "none-v1",
  "acknowledged_raw_history": true
}
```

The pre-launch `depot`, `corpus_dir`, and `accept_secrets_warning` keys are rejected rather than treated as aliases.

## R2 Archive

Create the bucket externally, issue a bucket-scoped Object Read & Write token, and keep credentials in the environment:

```bash
export AHA_R2_ACCOUNT_ID='...'
export AHA_R2_ACCESS_KEY_ID='...'
export AHA_R2_SECRET_ACCESS_KEY='...'

aha archive status 'r2:team-history'
aha archive init 'r2:team-history'       # only for a new, uninitialised bucket
aha archive set-default 'r2:team-history'
aha archive upload
aha archive download
```

Explicit one-off addresses must include `r2:` or `local:`. Configuration placeholders, conflicting aliases, unsafe local destinations, unreachable Archives, and Archive mismatches are rejected before mutation.

## Output contracts

- Final JSON is written only to stdout.
- Progress is written only to stderr; `--progress=json` emits NDJSON.
- Failures use the stable `aha.error.v1` envelope with one credential-safe next action.
- No error includes raw SDK, SQL, credential, or private-path causes.
- No progress output fabricates an ETA.

## Privacy and trust

Agent histories may contain prompts, source code, commands, tool output, credentials, and images. The Archive preserves raw history as provenance; treat Archives, Workspaces, exports from SQLite, logs, and screenshots as private. Redaction changes only the Workspace projection; the Archive preserves raw history.

- Source roots are read-only.
- Archive and Workspace destinations cannot overlap source roots.
- R2 credentials are environment-only.
- R2 preflight occurs before local Workspace mutation.
- Upload publication uses conditional writes and bounded retries.
- Download materialises every machine's latest complete snapshot and excludes historical manifests from the Workspace.

See [`docs/trust.md`](docs/trust.md) and [`docs/language-style.md`](docs/language-style.md).

## Development

```bash
make verify-quick
# CI-equivalent:
scripts/verify.sh ci
```

Every change follows red-green-refactor TDD and correctness by construction; see [`agents.md`](agents.md) and [`docs/correctness-by-construction-spec.md`](docs/correctness-by-construction-spec.md).

## Documentation

- [`docs/command-state-machine-v0.2-plan.md`](docs/command-state-machine-v0.2-plan.md) — accepted product/state model.
- [`docs/command-inventory.md`](docs/command-inventory.md) — concise command inventory.
- [`docs/commands.md`](docs/commands.md) — generated command metadata and JSON contracts.
- [`docs/onboarding.md`](docs/onboarding.md) — local and R2 setup.
- [`docs/user-journeys.md`](docs/user-journeys.md) — end-to-end journeys.
- [`docs/architecture.md`](docs/architecture.md) — implementation architecture.
- [`docs/trust.md`](docs/trust.md) — privacy and safety boundaries.
- [`docs/mcp-spec.md`](docs/mcp-spec.md) — read-only MCP surface.
- [`docs/serve-ui-spec.md`](docs/serve-ui-spec.md) — dashboard specification.
- [`docs/depot-v2-spec.md`](docs/depot-v2-spec.md) — historical internal storage-layout name retained by the implementation package.

## Licence

MIT; see `LICENSE`.
