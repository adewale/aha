# aha — TypeScript client

A typed TypeScript surface over the aha read-only MCP tools. Use it from any
code-mode runtime ([Cloudflare `@cloudflare/codemode`][cf],
[Anthropic code-execution-with-MCP][anth]) or from plain Node.

[cf]: https://blog.cloudflare.com/code-mode-mcp/
[anth]: https://www.anthropic.com/engineering/code-execution-with-mcp

## Layout

| Path                       | What it is                                                                                                |
| -------------------------- | --------------------------------------------------------------------------------------------------------- |
| `aha-mcp.ts`               | Generated typed surface: `Transport` interface, per-tool typed wrappers, all input/output type definitions. |
| `transports/stdio.ts`      | Node stdio transport — spawns `aha mcp` and frames JSON-RPC.                                              |
| `transports/http.ts`       | HTTP transport — talks to `aha serve` over `fetch`.                                                       |

`aha-mcp.ts` is regenerated from the Go types via `go run ./cmd/aha-gen-ts`.
Do not edit it by hand. The transport files are hand-written.

## Usage — stdio (spawn `aha mcp`)

```ts
import { spawn } from "node:child_process";
import { aha } from "./aha-mcp.js";
import { connectStdio } from "./transports/stdio.js";

const child = spawn("aha", ["mcp"]);
const transport = await connectStdio(child.stdin, child.stdout);
const tools = aha(transport);

const hits = await tools.search({ query: "migration bug", project: "billing", limit: 100 });
const userMessages = hits.filter(h => h.role === "user");
const contexts = await Promise.all(
  userMessages.slice(0, 5).map(h => tools.read({ ref: h.ref_text, before: 5, after: 30 })),
);

for (const entries of contexts) {
  for (const e of entries) {
    console.log(`[${e.timestamp}] ${e.role}: ${e.text.slice(0, 200)}`);
  }
}
```

## Usage — HTTP (point at `aha serve`)

```ts
import { aha } from "./aha-mcp.js";
import { connectHTTP } from "./transports/http.js";

// Defaults: aha serve listens on http://127.0.0.1:18428
const transport = connectHTTP("http://127.0.0.1:18428");
const tools = aha(transport);

const status = await tools.status();
console.log(`${status.sessions} sessions, ${status.entries} entries`);
```

## Usage — code-mode runtimes

Code-mode runtimes already expose a `Transport`-shaped tool proxy. Pass it to
`aha()` and you can write a single TypeScript program that fans out across the
corpus instead of issuing one MCP tool call at a time:

```ts
// inside a code-mode sandbox, where `mcp` is the runtime-provided tool proxy
import { aha } from "./aha-mcp.js";

const tools = aha({
  call: (name, args) => mcp.call("aha." + name, args),
});

const projects = await tools.search({ query: "rollback", role: "user", limit: 200 });
const grouped: Record<string, number> = {};
for (const hit of projects) {
  grouped[hit.project] = (grouped[hit.project] ?? 0) + 1;
}
console.log(JSON.stringify(grouped));
```

One MCP round trip, arbitrary intermediate logic. That's the win.

## Refs are the API

`search` returns `ref_text` on every result; pass it back to `read`. Refs are
deterministic across machines, so you can store them, share them, or pipe them
into another command. The discriminated union (`MessageRef | SessionRef |
ArtifactRef`) lets your code branch on `kind` without parsing strings.

## Errors

- Stdio transport: rejects with `Error("aha MCP error <code>: <message>")` for
  JSON-RPC errors; rejects with the underlying stream error on framing or
  socket failure.
- HTTP transport: rejects with `Error("<code>: <message>")` matching the
  server's pinned error envelope (`{ "error": { "code", "message" } }`).

Both transports surface `tools/call` validation failures (unknown argument,
missing required field, unknown tool) as plain `Error` objects with the
original message — your code can `try`/`catch` per call.

## Versioning

The TypeScript surface follows the aha CLI version. Regenerate it whenever you
upgrade the binary you're calling.
