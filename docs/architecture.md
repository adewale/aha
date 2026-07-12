# Architecture

## Product boundary

```text
Pi / Claude Code / Codex / OpenCode histories
                    │
                    │ archive upload
                    ▼
             Archive (local or R2)
                    │
                    │ archive download
                    ▼
             Workspace (local SQLite + blobs)
                    │
        search · show · analyse · dashboard · MCP
```

Authority flows in one direction. Agent histories are externally owned mutable inputs. The Archive is durable aggregated truth. A Workspace is a rebuildable local materialisation bound to one Archive.

## Archive storage

The current internal implementation remains in `internal/depot` while public vocabulary uses Archive.

```text
aha-depot.json
machines/index.json
machines/<machine>/latest
machines/<machine>/manifests/<manifest-sha>.json
blobs/v2/<content-sha>.zst
```

- Blobs and manifests are immutable and content-addressed.
- Each machine advances only its latest pointer.
- The machines index permits discovery without object-store LIST during steady-state operations.
- Pointer and index publication use conditional writes with bounded jittered retries.
- The Archive has no delete operation and no garbage collection in 0.2.

The marker contains a stable Archive identity. A validated marker and machines index construct an opaque reader capability. A validated marker constructs an opaque writer capability. Raw addresses cannot authorise publication or download.

## Upload

```text
parse input/config
  → inspect Archive marker
  → construct PreparedUpload
  → discover and stably capture source files
  → compare this machine with Archive latest
  → upload unknown blobs
  → publish immutable manifest
  → conditionally advance latest pointer/index
```

Upload never opens the Workspace. Its advisory capture cache lives outside the Workspace. An uninitialised Archive is rejected rather than silently initialised.

## Download

```text
parse input/config
  → validate Archive marker/index
  → freeze every machine's latest vector in DownloadPlan
  → validate Workspace destination ownership
  → inspect binding/vector read-only
  → reject mismatch or report current no-op
  → open Workspace
  → fetch only unknown blobs
  → parse/index snapshots
  → atomically record the materialised vector
```

The complete remote plan and destination preflight exist before the Workspace directory, SQLite database, WAL, or lifecycle lock can be created.

Cancellation before mutation has zero effects. Cancellation during ingestion may leave durable content already committed, but the materialised vector advances only after the full planned vector succeeds; status therefore reports the Workspace behind rather than falsely current.

## Workspace schema

In addition to immutable evidence and derived indexes, each Workspace stores:

```text
workspace_binding
  singleton = 1
  archive_identity
  archive_address
  schema_version

workspace_materialised
  machine_id → manifest_sha256
```

`workspace_binding` makes cross-Archive download structurally rejectable. `workspace_materialised` is the exact vector used for current/behind comparison. A checksummed `aha.workspace.identity.v1` witness outside SQLite retains the Workspace and Archive identities when SQLite is too damaged to read; unsupported witness or database schemas require an aha upgrade before mutation.

Raw/evidence tables include snapshots, machines, sources, files, sessions, session versions, entries, messages, artefacts, images, tool invocations, redaction events, and conflicts. Derived tables include FTS and path-token indexes.

## Repair

```text
damaged Workspace
  → exclusive lifecycle lock
  → build sibling from bound Archive
  → verify replacement
  → recursively sync replacement
  → atomic directory exchange
  → sync parent directory
  → retain previous Workspace as backup
```

No public no-backup mode exists. Platforms without safe atomic exchange reject repair.

## Closed lifecycle states

Archive:

```text
invalid_address · invalid_configuration · unreachable · uninitialised
empty · populated · damaged · upgrade_required
```

Workspace:

```text
absent · current · behind · damaged · archive_mismatch · invalid_destination
upgrade_required
```

The implementation centralises allowed transitions in typed state tables and exhaustively tests every state/operation pair. Rejected transitions include one next action.

## Read surfaces

- CLI processing commands open an existing Workspace and never create one.
- MCP and HTTP share the same read-only backend.
- Dashboard binding defaults to loopback and validates Host headers.
- Search produces stable refs; show resolves canonical refs or explicit session coordinates.
- FTS repair is explicit under `workspace verify --repair-fts`.

## Output boundaries

- stdout: final human or JSON result;
- stderr: progress and errors;
- `aha.error.v1`: one safe message, one next action, optional allowlisted diagnostics;
- no raw SQL, SDK, path, endpoint, or credential-bearing causes;
- no fabricated ETA.

## Compatibility boundaries

Config, Archive, Workspace, HTTP, MCP, and response schemas evolve independently. Archive write capabilities reject unknown required features; HTTP uses `/api/v2` and advertises `aha.http.v2`; MCP advertises `aha.mcp.v2`. Behaviour dates are reserved for intentional semantic gates rather than persisted byte formats. See [Compatibility policy](compatibility-policy.md).

## Build identity

`aha version --json` reports product version, linker-injected commit, build time, and dirty state. Development fallbacks remain explicit.
