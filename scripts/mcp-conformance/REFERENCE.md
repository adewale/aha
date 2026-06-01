# Reference server tool surface

Three reference MCP servers live in this directory and at
`cmd/aha-ref-mcp/`. They exist so the cross-SDK conformance suite can
drive our `clients/typescript/transports/stdio.ts` against a known-good
implementation in each major language SDK:

| File | SDK |
| --- | --- |
| `scripts/mcp-conformance/reference_server.py` | Anthropic `mcp` (Python FastMCP) |
| `scripts/mcp-conformance/reference_server.ts` | `@modelcontextprotocol/sdk` (TypeScript McpServer) |
| `cmd/aha-ref-mcp/main.go` | `github.com/modelcontextprotocol/go-sdk` (Go NewServer) |

## Canonical tool surface (THE CONTRACT)

All three servers MUST expose exactly these three tools with these
signatures. The TS conformance harness at
`clients/typescript/test/stdio.conformance.test.ts` round-trips all three
against each reference; a change to any of them must be made in all four
places (this doc plus the three implementations).

### `echo`

- **Input:** `{ text: string }`
- **Output:** the value of `text`, returned as the tool's text content
- **Purpose:** verifies a string round-trip across the wire and through
  the typed surface.

### `add`

- **Input:** `{ a: number, b: number }`
- **Output:** `a + b`, as a number (the text content is the number's
  decimal string representation; the TS transport JSON-parses it back to
  a number on the client side)
- **Purpose:** verifies typed numeric arguments and a non-string return.

### `fail`

- **Input:** `{}` (no arguments)
- **Output:** always raises an error with message "intentional"
- **Purpose:** verifies error propagation back through the transport.

## When to add or change a tool

Don't, in this directory. The reference servers exist to validate aha's
own client transport against the canonical SDK surfaces; they are not
intended to grow. If the conformance matrix needs more coverage,
extend `clients/typescript/test/stdio.conformance.test.ts` to exercise
more of the existing `echo`/`add`/`fail` surface first.

If you must add a tool — e.g., a new conformance dimension the
existing three don't cover — make the change in **all four places** in
a single commit:

1. This document (the contract).
2. `scripts/mcp-conformance/reference_server.py`.
3. `scripts/mcp-conformance/reference_server.ts`.
4. `cmd/aha-ref-mcp/main.go`.

The TS conformance harness then needs a parallel round-trip to exercise
the new tool against each of the three references.
