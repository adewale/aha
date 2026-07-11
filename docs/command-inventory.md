# Command inventory

This is the human command inventory for the current implementation: what each command is for and when to reach for it. Version 0.2 will deliberately replace this surface with the directional `push`/`pull`/`sync`/`import` and `store`/`library` model in [`command-state-machine-v0.2-plan.md`](command-state-machine-v0.2-plan.md); no compatibility aliases are planned. For generated usage strings, flags, examples, and JSON contracts, see `docs/commands.md`.

## Daily retrieval and analysis

| Command | Use when | Notes |
|---|---|---|
| `aha refresh` | Keep the local corpus current. | Common update path: snapshot configured sources or reuse an unchanged depot bundle, then ingest pending/new bundles. |
| `aha search` | Find leads in messages and artifacts. | Use `--json` or `--refs` for agents/scripts. Search snippets are leads, not evidence. |
| `aha read` | Retrieve evidence for a ref. | Pass a canonical `ref_text` from search or incidents. Use `--before`/`--after` to widen context. |
| `aha incidents` | Find recurring tool-call failure patterns and observed fix paths. | Use `--state unresolved` for pain/backlog and `--state resolved` for fixes worth harvesting into interventions. |
| `aha serve` | Browse the corpus locally. | Loopback dashboard with Search, Failures, and Sources journeys. |
| `aha mcp` | Let coding agents query the corpus without shelling out per call. | Read-only stdio MCP server. Use `--dry-run` to verify host wiring. |

## Capture and import

| Command | Use when | Notes |
|---|---|---|
| `aha init` | Write starter JSONC config. | `--accept-secrets` persists the v1 privacy acknowledgement. |
| `aha snapshot` | Push this machine's state to the depot without touching a corpus. | Uploads only new file versions and publishes a snapshot manifest; never downloads other machines' data (contribute-only machines). |
| `aha export` | Materialize a machine's latest depot snapshot as one portable `bundle.tar.zst`. | The single-file hand-off format; re-import anywhere with `aha ingest <file>`. |
| `aha ingest` | Pull depot snapshots into a corpus, or import v1 bundle files. | Depot pulls fetch only content the corpus does not already have. |

## Health, trust, and maintenance

| Command | Use when | Notes |
|---|---|---|
| `aha status` | Inspect corpus counts and health. | Check sessions, messages, snapshots, sources, redaction levels, and index size. |
| `aha verify` | Check corpus invariants. | Read-only by default; `--repair-fts` repairs derived FTS projections. |
| `aha conflicts` | Inspect quarantined merge conflicts. | Keeps conflict/trust issues out of normal search/read paths. |
| `aha doctor` | Diagnose config, source discovery, corpus, depot, and next actions. | May create/update the private OpenCode JSONL export cache while counting OpenCode sessions. |
| `aha corpus` | Inspect or maintain corpus storage. | Subcommands: `size`, `vacuum`, `prune-orphans`; destructive prune requires `--force`. |
| `aha depot` | Initialize, switch, list machine snapshots, or verify the durable store. | `depot verify` is quick by default; `--deep` verifies blob contents and historical manifests. |

## Typical workflows

### Keep history current

```bash
aha refresh
aha verify --json
```

### Search, then read evidence

```bash
aha search "migration bug" --refs
aha read <ref> --before 3 --after 10 --md
```

### Find intervention candidates

```bash
aha incidents --limit 50 --json
aha incidents --state resolved --json
aha incidents --state unresolved --json
aha read <sample_ref> --before 3 --after 10 --md
```

Then classify the pattern with `docs/patterns-and-interventions.md`:

- repeatable command/check sequence → runbook;
- reusable judgment or habit → skill;
- broad, parallel, uncertain work → dynamic workflow;
- repeated tool friction → tool/platform fix;
- high-pain unresolved pattern → investigation backlog.

### Let an agent query history

```bash
aha mcp --dry-run
aha mcp
```

Expose `aha mcp` in the host's MCP config. Agents should prefer `search`, `read`, `incidents`, `incident_trajectory`, `overview`, `status`, `verify`, `conflicts`, `corpus_size`, and `doctor`; write-side commands are intentionally not exposed through MCP.

## Global profiling

Any command can write local Go pprof profiles with `--cpuprofile FILE` and/or `--memprofile FILE`. These flags may appear before or after the subcommand, or be supplied via `AHA_CPU_PROFILE` and `AHA_MEM_PROFILE`.
