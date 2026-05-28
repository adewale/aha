# MCP spec

A read-only stdio MCP (Model Context Protocol) server that exposes the existing
read-side CLI surface (`search`, `read`, `status`, `verify`, `conflicts`,
`corpus size`, `doctor`) to coding agents without spawning a CLI subprocess per
call.

## Goals

- Let local coding agents call `aha` as MCP tools instead of shelling out.
- Reuse the existing JSON output shapes exactly. The MCP tools are a second
  skin over the same `internal/corpus`, `internal/search`, and `internal/depot`
  functions the CLI already calls.
- Stay read-only by default. State-changing operations (`init`, `refresh`,
  `snapshot`, `ingest`, `verify --repair-fts`, `corpus vacuum`,
  `corpus prune-orphans --force`, `depot init/compact/repair`) are deliberately
  not exposed in phase 1.
- Ship as a single subcommand: `aha mcp`. No new binary, no new dependency.

## Non-goals (phase 1)

- HTTP transport, SSE, WebSocket. Stdio only.
- Write tools. No ingest/snapshot/refresh.
- Code-mode TypeScript surface generation. Tracked separately; see "Phase 2"
  below.
- Dashboard. Tracked separately; see "Phase 3" below.
- Authentication. Same trust model as the CLI: whoever can run `aha mcp` can
  read the configured corpus.

## Tool surface

All tools accept JSON objects with strict argument validation (unknown keys
rejected) and return JSON whose shape matches the corresponding `--json` CLI
output.

| Tool          | Maps to                              | Input schema                                                                                                                                       | Output                                                |
| ------------- | ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| `search`      | `aha search`                         | `query` (string, required), `source`, `machine`, `role`, `after`, `before`, `path`, `path_token`, `project`, `limit` (int, ≤ `search.MaxLimit`)    | `[]search.Result`                                     |
| `read`        | `aha read`                           | one of: `ref` (string, canonical ref text) **or** `session` + optional `entry`; plus `before` (int, default 3), `after` (int, default 5)           | `[]corpus.ReadEntry`                                  |
| `status`      | `aha status`                         | none                                                                                                                                               | `map[string]any` from `corpus.Status`                 |
| `verify`      | `aha verify` (read-only; no repair)  | none                                                                                                                                               | `corpus.VerifyReport`                                 |
| `conflicts`   | `aha conflicts`                      | none                                                                                                                                               | `[]corpus.Conflict`                                   |
| `corpus_size` | `aha corpus size`                    | none                                                                                                                                               | `corpus.SizeReport`                                   |
| `doctor`      | `aha doctor` (local-only by default) | none                                                                                                                                               | `{version, config, adapters, sources, corpus, next}`  |

`doctor` skips the depot probe in phase 1 to keep the MCP surface local-only
and free of network calls.

## Protocol

JSON-RPC 2.0 over stdio with LSP-style `Content-Length:` framing. This is the
same wire format coding-agent hosts already speak to MCP servers and exactly
matches the framing tracebase uses.

Supported methods:

- `initialize` → returns `{protocolVersion, capabilities: {tools: {}}, serverInfo: {name: "aha", version}}`.
- `notifications/initialized` → no response.
- `ping` → returns `{}`.
- `tools/list` → returns `{tools: [...]}` (the table above).
- `tools/call` → returns `{content: [{type: "text", text: <json>}]}`.
- `resources/list` → returns `{resources: []}` (none in phase 1).

Errors use standard JSON-RPC error codes: `-32600` invalid request, `-32601`
unknown method, `-32000` tool error.

## Process lifecycle

```
aha mcp [--config PATH] [--repo DIR]
```

- Opens the corpus once at startup (read-only). The corpus is reused across
  every `tools/call` for the life of the process.
- Reads JSON-RPC frames from stdin. Writes responses to stdout.
- Status, errors, and any diagnostics go to stderr (never to stdout — stdout is
  the protocol channel).
- Exits cleanly when stdin closes.

## Security boundaries

- Stdio only. No port is opened, no socket is bound.
- Read-only: only `search`, `read`, `status`, `verify`, `conflicts`,
  `corpus_size`, and `doctor` are reachable; write tools are not registered.
- Same filesystem access as the CLI: whatever corpus and config the calling
  user can read.
- No depot writes, no R2 calls, no remote network access in phase 1.
- Strict argument validation: unknown keys are rejected; required keys are
  enforced before invoking the underlying function.
- Output uses the same JSON shapes the CLI already emits, so the existing JSON
  contract tests cover the wire shape.

## Reuse vs duplication

The MCP server calls the same `internal/corpus`, `internal/search`, and (for
`doctor`) `internal/adapters` functions that the CLI calls. No business logic
is duplicated. The MCP layer is purely:

1. JSON-RPC framing and dispatch.
2. Argument validation.
3. Invocation of an existing read function.
4. Marshalling the result to JSON.

## Test strategy

- Protocol tests: frame encode/decode roundtrip; truncated input;
  multi-message buffer.
- Dispatch tests: `tools/list` returns the expected tool set; `tools/call`
  with unknown name returns `-32000`; unknown args rejected.
- Integration: build a temporary corpus from a fixture, run `tools/call` for
  each tool against it, assert the JSON payload matches the corresponding
  `--json` CLI output. This reuses the same fixture loaders the CLI tests use
  and ensures the two surfaces stay in sync.
- The existing `internal/cli/json_contract_test.go` continues to guard the
  underlying shapes; if those change, both CLI and MCP move together.

## Phase 2 — code-mode adapter (shipped)

`internal/mcp/codegen` reflects on the Go return types of each tool and emits
a typed TypeScript surface at `clients/typescript/aha-mcp.ts`. Regenerate via
`go run ./cmd/aha-gen-ts` after changing any of the Go shapes; a drift test in
`internal/mcp/codegen/codegen_test.go` fails CI if the checked-in file is
stale.

The TS surface defines:

- `MessageRef | SessionRef | ArtifactRef` discriminated union mirroring the
  `internal/model.Ref` JSON shapes.
- One interface per Go struct (SearchResult, ReadEntry, VerifyReport, etc.)
  with optional fields driven by Go's `omitempty` tags.
- A `Transport` interface (`call(name, args): Promise<unknown>`) and an
  `aha(transport)` factory that returns one typed async function per tool.

This is transport-agnostic. A stdio MCP client, the phase-3 HTTP server, or a
code-mode runtime (Cloudflare `@cloudflare/codemode`, Anthropic
code-execution-with-MCP) can all plug a `Transport` in and write
type-checked agent code such as:

```ts
const tools = aha(transport);
const hits = await tools.search({ query: "migration", project: "billing", limit: 100 });
const refs = hits.filter(h => h.role === "user").map(h => h.ref_text);
const contexts = await Promise.all(refs.slice(0, 5).map(r =>
  tools.read({ ref: r, after: 30 })
));
```

## Phase 3 — dashboard (shipped)

`aha serve` mounts the same tool surface behind an HTTP handler in
`internal/server`, served with a minimal vanilla HTML+JS UI embedded via
`go:embed`. No React, no Vite, no Node runtime on the host — the dashboard
ships inside the same static Go binary as the rest of the CLI.

Routes:

| Method | Path                | Body / Output                          |
| ------ | ------------------- | -------------------------------------- |
| GET    | `/`                 | embedded `index.html`                  |
| GET    | `/static/*`         | embedded `app.js`, `app.css`           |
| GET    | `/api/status`       | corpus status JSON                     |
| GET    | `/api/verify`       | corpus verify report                   |
| GET    | `/api/conflicts`    | quarantined conflicts                  |
| GET    | `/api/corpus_size`  | disk-usage report                      |
| GET    | `/api/doctor`       | local diagnostics                      |
| GET    | `/api/version`      | `{version}`                            |
| GET    | `/api/tools`        | `{tools: [...]}` (advertised surface)  |
| POST   | `/api/search`       | JSON args → `[]SearchResult`           |
| POST   | `/api/read`         | JSON args → `[]ReadEntry`              |

Security posture:

- Loopback bind by default (`127.0.0.1:18428`). Non-loopback addresses are
  refused unless `--allow-remote` (or `AHA_ALLOW_REMOTE=1`) is set.
- Same dispatch as MCP (`mcp.CallTool`), so the same strict argument
  validation applies. Unknown keys are rejected with a 400.
- All routes are read-only. There is no write surface in phase 1.
- No CORS headers; this is a single-origin local UI.

## Host integration

`aha mcp` reads from stdin and writes to stdout, so any MCP host that can
spawn a local subprocess can drive it.

### Claude Desktop / Cursor / Continue / Codex

Add an entry to the host's MCP config file (path varies per host; for Claude
Desktop on macOS it's `~/Library/Application Support/Claude/claude_desktop_config.json`):

```jsonc
{
  "mcpServers": {
    "aha": {
      "command": "aha",
      "args": ["mcp"]
    }
  }
}
```

Optional flags:

- `["mcp", "--config", "/path/to/aha.jsonc"]` to pin a non-default config.
- `["mcp", "--repo", "/path/to/corpus"]` to point at a specific corpus.

The host reads tool descriptions via `tools/list` and invokes tools via
`tools/call`. No further setup is required.

### Code-mode hosts (Cloudflare codemode, Anthropic code-execution-with-MCP)

Wire `aha mcp` as a stdio MCP server in the host's tool registry, then import
the typed TS surface from `clients/typescript/aha-mcp.ts` and pass the
host-provided tool proxy as the `Transport`. See
`clients/typescript/README.md` for end-to-end examples.

### Troubleshooting

| Symptom                                       | Likely cause                                                    |
| --------------------------------------------- | --------------------------------------------------------------- |
| Server exits immediately with stat error      | Corpus has not been built; run `aha refresh` first.             |
| `frame parse error: invalid Content-Length`   | Host is sending NDJSON; not supported in phase 1.                |
| `unknown tool: refresh` (or similar) over MCP | Write tools are intentionally not exposed; use the CLI directly. |
| Dashboard returns 421 Misdirected Request     | Host header doesn't match the loopback allowlist; use `localhost`, `127.0.0.1`, or set `--allowed-hosts`. |
| Dashboard returns 415 Unsupported Media Type  | POST body sent without `Content-Type: application/json`.        |

## Open questions

- Should `doctor` accept an opt-in `depot` arg in phase 1 or stay strictly
  local? Default is "no" (local-only).
- Should `read` accept an explicit `format` arg (`json` vs `md`)? Phase 1 only
  returns the structured `[]ReadEntry`; markdown can be a phase 2 add.
- Should `tools/list` advertise tool annotations (`readOnlyHint: true`) once
  the spec stabilizes? Phase 1 declares read-only in the description string;
  hint metadata is a phase 2 add.
