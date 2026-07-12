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
| `transports/http.ts`       | HTTP transport — talks to `aha dashboard` over `fetch`.                                                       |

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
  userMessages.slice(0, 5).map(h => tools.show({ ref: h.ref_text, before: 5, after: 30 })),
);

for (const entries of contexts) {
  for (const e of entries) {
    console.log(`[${e.timestamp}] ${e.role}: ${e.text.slice(0, 200)}`);
  }
}
```

## Usage — HTTP (point at `aha dashboard`)

```ts
import { aha } from "./aha-mcp.js";
import { connectHTTP } from "./transports/http.js";

// Defaults: aha dashboard listens on http://127.0.0.1:18428
const transport = connectHTTP("http://127.0.0.1:18428");
const tools = aha(transport);

const status = await tools.status();
console.log(`${status.sessions} sessions, ${status.entries} entries`);
```

## Usage — code-mode runtimes

Code-mode runtimes already expose a `Transport`-shaped tool proxy. Pass it to
`aha()` and you can write a single TypeScript program that plans, filters, and
fans out across the corpus over a long-lived transport:

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

One code-mode program, arbitrary intermediate logic. The fan-out still performs
one MCP tool call per `show`; use `Promise.all` when you want parallelism.

## Refs are the API

`search` returns `ref_text` on every result; pass it back to `show`. Refs are
deterministic across machines, so you can store them, share them, or pipe them
into another command. The discriminated union (`MessageRef | SessionRef |
ArtifactRef`) lets your code branch on `kind` without parsing strings.

## Errors

The stdio transport throws `AhaMcpError` (exported from `transports/stdio.ts`)
with a stable `code` field, so callers can branch on the failure mode
without regex-matching message text:

```ts
import { AhaMcpError } from "./transports/stdio.js";

try {
  await tools.show({ ref: someRef });
} catch (e) {
  if (e instanceof AhaMcpError) {
    if (e.code === "tool_error")        { /* aha returned isError: a missing ref, bad query, etc. */ }
    else if (e.code === "transport_closed") { /* aha mcp exited; respawn */ }
    else                                { /* wire-level JSON-RPC error; e.code is the numeric code stringified */ }
  }
}
```

`AhaMcpError.code` values:

- `"tool_error"` — the server returned `isError: true` on the
  `CallToolResult` (unknown argument, missing required field, invalid ref,
  etc.). The error's `message` is the server's `content[0].text`.
- `"transport_closed"` — `aha mcp` exited or the stdio stream errored.
- A numeric string (e.g. `"-32601"`) — a JSON-RPC error code from the
  wire (method not found, parse error, etc.).

The HTTP transport rejects with a plain `Error("<code>: <message>")`
matching the server's pinned error envelope; symmetry with the stdio
class is a follow-up.

## How to wire aha into a code-mode runtime

The pattern is the same across runtimes: the host owns the executor (the
isolate, vm, subprocess, container, etc.); aha ships the typed surface
the agent's TypeScript runs against. The `Transport` interface
(`aha-mcp.ts`) is the seam.

### Cloudflare `@cloudflare/codemode`

Cloudflare's `DynamicWorkerExecutor` runs the agent's generated TS in a
V8 isolate with no FS / no env / `globalOutbound: null`. Pass aha as a
`ToolProvider` whose dispatcher implements the `Transport.call` shape:

```ts
import { createCodeTool, type ToolProvider } from "@cloudflare/codemode";
import { aha as ahaFactory } from "./aha-mcp.js";
import { connectStdio } from "./transports/stdio.js";

// In the host (outside the sandbox):
const child = spawn("aha", ["mcp"]);
const transport = await connectStdio(child.stdin, child.stdout);

const provider: ToolProvider = {
  name: "aha",
  // Hand-write the TS surface from the checked-in aha-mcp.ts; Cloudflare's
  // codemode embeds it in the agent's tool description.
  types: await readFile("./aha-mcp.ts", "utf8"),
  dispatcher: {
    call: (toolName, argsJson) => transport.call(toolName, JSON.parse(argsJson)),
  },
};

const tools = [createCodeTool({ tools: [provider] })];
// Hand `tools` to whatever model/agent SDK you use; the model writes TS like:
//   const tools = aha(codemode); const hits = await tools.search({...}); ...
```

What the host is responsible for: starting / restarting the `aha mcp`
subprocess, supplying the `Executor` (Cloudflare ships
`DynamicWorkerExecutor` and `IframeSandboxExecutor`; you can implement
`Executor` for `node:vm`, QuickJS, containers, etc.), and applying the
sandbox's egress / FS policy. aha owns nothing inside the sandbox.

### Anthropic code-execution-with-MCP

Anthropic's runtime expects each MCP tool to be a thin TS module on a
sandbox filesystem; the agent reads them with `ls`+`cat` on demand. The
mechanical adapter is:

```ts
// servers/aha/index.ts (one of these per tool, generated from aha-mcp.ts)
export async function search(input: SearchArgs): Promise<SearchResult[]> {
  return callMCPTool<SearchResult[]>("aha__search", input);
}
```

`callMCPTool` is the host-provided helper that bridges to whichever MCP
transport the host configured (`aha mcp` over stdio, in our case). The
host runs the agent's code inside Anthropic's hosted sandbox (Python
3.11.12, 5 GiB RAM, no network) or a local `sandbox-runtime` (bubblewrap
on Linux, sandbox-exec on macOS).

What the host is responsible for: the sandbox itself, the
`callMCPTool` bridge, and lazy filesystem discovery of the tool modules.
aha owns only the typed wrappers.

## What aha provides and what the host provides

```
   ┌────────────────────────┐         ┌────────────────────────┐
   │   Code-mode runtime    │         │       aha (this        │
   │   (Cloudflare,         │         │       repository)      │
   │   Anthropic, custom)   │         │                        │
   ├────────────────────────┤         ├────────────────────────┤
   │ Sandbox / executor     │         │ aha mcp (Go server)    │
   │   - V8 isolate or vm   │         │   - 10 read-only tools │
   │   - no FS / no egress  │ <─────> │   - SDK-driven wire    │
   │ Tool surface injection │  stdio  │                        │
   │   - generates the      │ JSON    │ Typed TS surface       │
   │     `aha` namespace    │ RPC     │   - aha-mcp.ts         │
   │     visible to the LLM │         │   - parseRef/formatRef │
   │ Execution loop         │         │   - AhaMcpError class  │
   │   - runs the agent's   │         │ Transports             │
   │     TS, captures logs  │         │   - stdio (this repo)  │
   └────────────────────────┘         │   - http (this repo)   │
                                       └────────────────────────┘
```

The seam is the `Transport` interface. Anything that can implement
`{ call(name, args): Promise<unknown> }` can drive `aha()`. The host
chooses the sandbox; aha never tries to.

## Versioning

The TypeScript surface follows the aha CLI version. Regenerate it whenever you
upgrade the binary you're calling.
