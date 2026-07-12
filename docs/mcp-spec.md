# MCP specification

Aha exposes a read-only MCP server over one existing Workspace.

```bash
aha mcp check [--workspace PATH]
aha mcp serve [--workspace PATH]
```

`check` opens the Workspace, constructs the SDK server, registers every tool, prints one diagnostic line to stderr, and exits without reading stdin. `serve` runs JSON-RPC 2.0 over NDJSON-framed stdio.

## Transport boundary

- stdin/stdout belong exclusively to MCP protocol frames;
- human diagnostics use stderr;
- the official Go MCP SDK owns framing, schema validation, tool registration, ping, and `initialize` negotiation;
- tool handlers are read-only and cannot publish, download, repair, or change config.

## Tools

The canonical names are generated from `internal/mcp.ToolNames` and shared with the TypeScript client.

| Tool | Purpose |
|---|---|
| `search` | search Workspace messages and artefacts, returning stable refs |
| `read` | retrieve window, branch, or live context for a canonical ref/session coordinate |
| `status` | return Workspace counts and health |
| `verify` | run read-only Workspace invariant checks |
| `conflicts` | list quarantined merge conflicts |
| `corpus_size` | return local Workspace disk usage (legacy wire name retained for MCP schema compatibility) |
| `doctor` | return local-only environment/config/source/Workspace diagnostics (legacy wire name retained) |
| `incidents` | return recurring failure groups and resolution paths |
| `incident_trajectory` | reconstruct a fail-to-fix arc from a sample resolving ref |
| `overview` | return Workspace composition and time/source/project summaries |

These wire names are not CLI aliases. CLI processing uses `show`, `analyse failures`, resource-scoped status/verify/conflicts, and explicit `mcp check|serve`.

## Safety

- The server opens an existing Workspace; it never creates one.
- Unknown input properties are rejected by generated schemas.
- Query limits are bounded.
- Canonical refs are parsed before database access.
- Errors are normalised through the same credential-safe boundary as CLI/HTTP surfaces.
- R2 Archive probing is intentionally absent from MCP tools.

## Example host configuration

```json
{
  "command": "aha",
  "args": ["mcp", "serve", "--workspace", "/path/to/workspace"]
}
```

Before configuring a host:

```bash
aha mcp check --workspace /path/to/workspace
```

## Code generation

```bash
go run ./cmd/aha-gen-ts -out clients/typescript/aha-mcp.ts
```

The generated TypeScript interface, tool descriptions, schemas, and version are checked for drift in CI.
