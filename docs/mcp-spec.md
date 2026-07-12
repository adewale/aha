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

## Contract and tools

The contract is `aha.mcp.v2`. `aha_capabilities` returns that schema, required client features, and the exact tool set. Canonical names come from `internal/mcp.ToolNames` and are shared with the TypeScript client.

| Tool | Purpose |
|---|---|
| `aha_capabilities` | negotiate the MCP contract before relying on optional behaviour |
| `search` | search Workspace messages and artefacts, returning stable refs |
| `show` | retrieve window, branch, or live context for a canonical ref/session coordinate |
| `status` | return Workspace counts and health |
| `workspace_verify` | run read-only Workspace invariant checks |
| `workspace_conflicts` | list quarantined merge conflicts |
| `workspace_size` | return local Workspace disk usage |
| `analyse_failures` | return recurring failure groups and resolution paths |
| `analyse_failure_trajectory` | reconstruct a fail-to-fix arc from a sample resolving ref |
| `overview` | return Workspace composition and time/source/project summaries |

The pre-launch wire names were removed rather than aliased. Tools are additive within `aha.mcp.v2`; a removed field, changed type, or changed meaning requires a new contract.

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
