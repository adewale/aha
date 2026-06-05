# MCP spec

A read-only stdio MCP (Model Context Protocol) server that exposes the existing
read-side CLI surface (`search`, `read`, `clusters`, `status`, `verify`,
`conflicts`, `corpus size`, `doctor`) to coding agents without spawning a CLI subprocess per
call.

## Goals

- Let local coding agents call `aha` as MCP tools instead of shelling out.
- Reuse the existing JSON output shapes exactly. The MCP tools are a second
  skin over the same `internal/corpus`, `internal/search`, and `internal/depot`
  functions the CLI already calls.
- Stay read-only by default. State-changing operations (`init`, `refresh`,
  `snapshot`, `ingest`, `verify --repair-fts`, `corpus vacuum`,
  `corpus prune-orphans --force`, `depot init/use/compact`,
  `depot verify --repair`) are
  deliberately not exposed.
- Ship as a single user-facing subcommand: `aha mcp`.

> The original goal also read "no new binary, no new dependency". Both were
> later retracted with deliberate rationale: aha depends on
> `github.com/modelcontextprotocol/go-sdk` v1.6.1 so the wire format is
> someone else's problem (see "Protocol" below), and `cmd/aha-ref-mcp` is a
> test-only Go-SDK reference server used by the cross-SDK conformance suite
> (see "Client-under-test" below). Neither is reachable from the production
> CLI surface.

## Non-goals

- Write tools. No ingest/snapshot/refresh.
- HTTP transport. The dashboard at `internal/server` is HTTP+REST, not
  MCP-over-HTTP; the MCP surface is stdio only.
- Authentication on the MCP transport. Same trust model as the CLI:
  whoever can run `aha mcp` can read the configured corpus. The HTTP
  dashboard has its own bearer-token model documented in
  `internal/server/server.go`.

## Tool surface

All tools accept JSON objects with strict argument validation (unknown keys
rejected) and return JSON whose shape matches the corresponding `--json` CLI
output.

| Tool          | Maps to                              | Input schema                                                                                                                                       | Output                                                |
| ------------- | ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------- |
| `search`      | `aha search`                         | `query` (string, required), `source`, `machine`, `role`, `after`, `before`, `path`, `path_token`, `project`, `limit` (int, ≤ `search.MaxLimit`)    | `[]search.Result`                                     |
| `read`        | `aha read`                           | one of: `ref` (string, canonical ref text) **or** `session` + optional `entry`; plus `before` (int, default 3), `after` (int, default 5)           | `[]corpus.ReadEntry`                                  |
| `clusters`    | `aha clusters`                       | `limit` (int, default 50)                                                                                                                          | `[]corpus.Cluster`                                    |
| `skill_candidates` | `aha clusters --with-fixes`     | `limit` (int, default 50)                                                                                                                          | `[]corpus.SkillCandidate`                             |
| `incidents`   | (dashboard unified view)             | `limit` (int, default 50); optional `project`, `source`, `machine`, `tool`                                                                          | `[]corpus.Incident`                                   |
| `incident_trajectory` | (dashboard drill-in)         | `ref` (string, a resolving-success `msg:v1:` ref)                                                                                                   | `[]corpus.TrajectoryStep`                             |
| `overview`    | (dashboard orientation)              | none                                                                                                                                               | `corpus.Overview`                                     |
| `status`      | `aha status`                         | none                                                                                                                                               | `map[string]any` from `corpus.Status`                 |
| `verify`      | `aha verify` (read-only; no repair)  | none                                                                                                                                               | `corpus.VerifyReport`                                 |
| `conflicts`   | `aha conflicts`                      | none                                                                                                                                               | `[]corpus.Conflict`                                   |
| `corpus_size` | `aha corpus size`                    | none                                                                                                                                               | `corpus.SizeReport`                                   |
| `doctor`      | `aha doctor` (local-only by default) | none                                                                                                                                               | `{version, config, adapters, sources, corpus, next}`  |

`doctor` skips the depot probe to keep the MCP surface local-only and free
of network calls. This is a permanent design decision, not a deferral: the
CLI's `aha doctor` retains `--depot` for the same surface, separately.

## Protocol

aha runs the official [`github.com/modelcontextprotocol/go-sdk`](https://github.com/modelcontextprotocol/go-sdk)
Server (v1.6+). All wire-format details — JSON-RPC 2.0 framing, the
newline-delimited stdio transport, version negotiation, lifecycle methods,
`tools/list` shape, `tools/call` envelope, `structuredContent` vs
`content[].text` decisions, ping, progress, cancellation, error
propagation — are handled by the SDK. We register the eight read tools
via `mcp.AddTool[In, Out]` with typed Go input structs and let the SDK
generate JSON-Schema, marshal results, and emit conformant responses.

The hand-rolled framer that lived in `internal/mcp/protocol.go` through
late May 2026 was retired in this migration. It survived an NDJSON fix
(the original Content-Length framing was a tracebase artefact that real
MCP clients can't decode) and ~6 commits before the official Go SDK
reached the maturity we wanted to lean on. Spec-version drift, structured
output, output schemas, tool annotations, and the "what does the wire
look like" maintenance are now somebody else's problem.

Negotiated protocol version: whatever the SDK currently defaults to (as
of v1.6.1, `2025-11-25`). Earlier revisions are still accepted via
version negotiation.

Supported methods (all SDK-provided):

- `initialize` / `notifications/initialized` — handshake
- `ping` — keepalive
- `tools/list` / `tools/call` — tool surface
- `resources/list` (empty) — capability advertisement; aha does not
  expose MCP resources today.

## Process lifecycle

```
aha mcp [--config PATH] [--repo DIR] [--dry-run]
```

- Opens the corpus once at startup (read-only). The corpus is reused across
  every `tools/call` for the life of the process.
- Reads JSON-RPC frames from stdin. Writes responses to stdout.
- Status, errors, and any diagnostics go to stderr (never to stdout — stdout is
  the protocol channel).
- Exits cleanly when stdin closes.
- `--dry-run` opens the corpus, registers every tool, prints a one-line
  summary to stderr, and exits 0 without reading stdin. Useful as a
  pre-flight check from a host config: confirms the binary can find its
  corpus and that the advertised tool set is what the host expects.

## Security boundaries

- Stdio only. No port is opened, no socket is bound.
- Read-only: only `search`, `read`, `clusters`, `skill_candidates`,
  `incidents`, `incident_trajectory`, `overview`, `status`, `verify`,
  `conflicts`, `corpus_size`, and `doctor` are reachable; write tools are not registered.
- Same filesystem access as the CLI: whatever corpus and config the calling
  user can read.
- No depot writes, no R2 calls, no remote network access.
- Strict argument validation: unknown keys are rejected; required keys are
  enforced before invoking the underlying function.
- Output uses the same JSON shapes the CLI already emits, so the existing JSON
  contract tests cover the wire shape.

## Reuse vs duplication

The MCP server calls the same `internal/corpus`, `internal/search`, and (for
`doctor`) `internal/adapters` functions that the CLI calls. No business logic
is duplicated. The MCP layer is purely:

1. SDK-provided framing, dispatch, and `additionalProperties: false`
   enforcement against the auto-derived schema for each typed input.
2. Typed input structs with `jsonschema` tags (the SDK derives JSON-Schema
   including the strict-additional-properties enforcement above).
3. Invocation of the pure `do<Tool>` business function.
4. SDK `CallToolResult` construction. Object-typed tools (`status`,
   `verify`, `corpus_size`, `doctor`) return a typed `Out` and let the
   SDK fill both `content[].text` and `structuredContent` from one
   marshal. List-typed tools (`search`, `read`, `clusters`, `conflicts`) use
   `Out=any` because the SDK refuses array output schemas, and call a
   thin `textResult` helper to set `content[].text` manually.

The same `CallTool` dispatch is exported and reused by the HTTP dashboard
(`internal/server`), which doesn't go through the MCP wire.

## Test strategy

- Wire format, framing, lifecycle, version negotiation, and structured
  output are owned by the SDK and tested in its own
  [conformance suite](https://github.com/modelcontextprotocol/go-sdk/tree/main/mcp).
  aha does not duplicate those tests.
- In-process dispatch tests (`internal/mcp/tools_test.go`) construct an
  `NewInMemoryTransports` Client↔Server pair, run `tools/list` and assert
  the canonical `mcp.ToolNames` set with `readOnlyHint: true` annotations,
  round-trip `tools/call` for object-returning and list-returning tools,
  and verify error paths fire as `isError` on `CallToolResult` rather than
  JSON-RPC error codes (the spec-compliant behaviour).
- HTTP↔MCP consistency (`TestHTTPAndMCPPathsAreConsistent`): the same
  business calls dispatched through both paths against one shared backend
  must produce semantically equal payloads. Future refactors that touch
  one path silently break here.
- Tool list drift (`TestCanonicalToolListReferencedByConformanceScripts`):
  every cross-language conformance harness must reference each name in
  `mcp.ToolNames`. Adding a tool without updating a script fails CI.
- Generated TS surface drift (`internal/mcp/codegen/codegen_test.go`):
  the checked-in `clients/typescript/aha-mcp.ts` is regenerated and
  byte-compared.
- The existing `internal/cli/json_contract_test.go` continues to guard the
  underlying shapes; if those change, both CLI and MCP move together.

## Cross-implementation conformance (8 checks)

`scripts/verify.sh mcp` runs the full cross-SDK conformance matrix. Three
official MCP SDKs (Python `mcp`, TypeScript `@modelcontextprotocol/sdk`, Go
`github.com/modelcontextprotocol/go-sdk`) × two directions = six
independent legs, plus a Code Mode workflow leg that exercises the typed
TS surface end-to-end and an in-process HTTP↔MCP consistency check that
pins both internal paths to the same payload. Optional SDK legs skip gracefully
when their toolchain isn't available, but the Go and Node runtime legs run on
standard CI runners and fail on tool-surface drift.

### Server-under-test (drive `aha mcp` from a real SDK Client)

| Leg                 | Driver                                                            |
| ------------------- | ----------------------------------------------------------------- |
| Python SDK client   | `scripts/mcp-conformance/client_against_aha.py` (`mcp.ClientSession` + stdio_client) |
| TypeScript SDK client | `scripts/mcp-conformance/client_against_aha.ts` (`Client` + `StdioClientTransport`) |
| Go SDK client       | `internal/mcp/conformance/go_sdk_test.go` (`mcp.NewClient` + `CommandTransport`)     |

Each leg makes the same assertions:

- handshake completes; `serverInfo.name == "aha"`;
- `tools/list` returns the exact expected set;
- every tool advertises `readOnlyHint: true`;
- `tools/call status` returns an object payload; when `structuredContent`
  is present it matches the text payload;
- `tools/call search` round-trips a list-shaped payload;
- empty results serialize as `[]`, not `null`;
- unknown args and unknown tools surface as errors.

### Client-under-test (drive our `connectStdio` against a real SDK server)

| Leg                            | Reference server                                                     |
| ------------------------------ | -------------------------------------------------------------------- |
| Python FastMCP reference       | `scripts/mcp-conformance/reference_server.py` (FastMCP, 3 tools)     |
| TypeScript McpServer reference | `scripts/mcp-conformance/reference_server.ts` (TS SDK McpServer)     |
| Go SDK reference               | `cmd/aha-ref-mcp/main.go` (Go SDK NewServer, built as `aha-ref-mcp`) |

Each reference exposes the same three tools (`echo`, `add`, `fail`) and the
TS conformance suite at `clients/typescript/test/stdio.conformance.test.ts`
round-trips all three against each reference, asserting payload equality
and error propagation.

### Why three SDKs

Testing the framer against itself proves nothing. Testing the server
against one SDK closes most of the gap. Testing across three SDKs catches
implementation-specific assumptions the spec doesn't pin: the Python SDK's
Pydantic types reject `structuredContent` arrays; the TS SDK accepts both
parsed and string payloads through different code paths; the Go SDK
exposes `Annotations.ReadOnlyHint` as a value where the TS SDK uses a
nullable boolean. A bug visible to only one SDK is still a real bug; the
matrix surfaces it.

### Code Mode workflow conformance

`scripts/mcp-conformance/codemode_workflow.ts` exercises the typed surface
the way a code-mode runtime ([Cloudflare codemode][cf], [Anthropic
code-execution-with-MCP][anth]) would:

[cf]: https://blog.cloudflare.com/code-mode-mcp/
[anth]: https://www.anthropic.com/engineering/code-execution-with-mcp

```ts
const tools = aha(transport);
const hits = await tools.search({ query: "hello", limit: 20 });
const refs = hits.filter(h => h.role === "user").map(h => h.ref_text);
const contexts = await Promise.all(
  refs.slice(0, 5).map(r => tools.read({ ref: r, before: 1, after: 3 }))
);
```

A green run proves that:

1. `cmd/aha-gen-ts` still generates a typed surface from the migrated
   server's Go types.
2. The typed surface compiles and runs against the SDK-backed server.
3. The canonical fan-out + fan-in pattern (search → local filter →
   parallel read) works end-to-end with no manual JSON handling on the
   caller side.

This is the load-bearing claim for code-mode runtime compatibility: one
code-mode program can plan, filter, and fan out over a long-lived transport.
The fan-out still performs one MCP tool call per `read`; a future batch-read
tool would be needed for a literal single MCP round trip.

## Code-mode TypeScript adapter

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

This is transport-agnostic. A stdio MCP client, the HTTP dashboard's REST
surface at `aha serve`, or a code-mode runtime (Cloudflare
`@cloudflare/codemode`, Anthropic code-execution-with-MCP) can all plug a
`Transport` in and write type-checked agent code such as:

```ts
const tools = aha(transport);
const hits = await tools.search({ query: "migration", project: "billing", limit: 100 });
const refs = hits.filter(h => h.role === "user").map(h => h.ref_text);
const contexts = await Promise.all(refs.slice(0, 5).map(r =>
  tools.read({ ref: r, after: 30 })
));
```

## HTTP dashboard

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
| POST   | `/api/clusters`     | JSON args → `[]Cluster`                |
| POST   | `/api/skill_candidates` | JSON args → `[]SkillCandidate`     |
| POST   | `/api/incidents`    | JSON args → `[]Incident`               |
| POST   | `/api/incident_trajectory` | JSON args → `[]TrajectoryStep`  |
| GET    | `/api/overview`     | `Overview`                             |

Security posture:

- **Loopback bind by default** (`127.0.0.1:18428`). Non-loopback and wildcard
  addresses (including `:PORT`) are refused at `Listen` time unless
  `--allow-remote` (or `AHA_ALLOW_REMOTE=1`) is set.
- **Bearer-token authentication required when remote.** `--token <TOKEN>`
  (or `AHA_DASHBOARD_TOKEN`) is enforced on every request via constant-time
  comparison; `WWW-Authenticate: Bearer realm="aha"` accompanies 401
  responses. `Listen` itself refuses `--allow-remote` without `--token` so
  a non-loopback dashboard cannot start unauthenticated.
- **Host header allowlist.** Every request's `Host` header is validated
  against the loopback set (`localhost`, `127.0.0.1`, `[::1]`, `::1`) plus
  any entries supplied via `--allowed-hosts H1,H2`. Foreign hosts get
  421 Misdirected Request. Numeric ports are required (`127.0.0.1:abc`
  is rejected); IDN homographs are rejected; malformed IPv6 bracket
  shapes are rejected. Defends against DNS-rebind attacks.
- **JSON Content-Type enforcement.** POST routes return 415 unless
  `Content-Type: application/json` (with optional charset parameter) is
  present. Stops cross-origin "simple" form posts from reaching the
  JSON dispatcher.
- **Strict Content-Security-Policy** on the index:
  `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'`.
  Plus `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`.
- **Per-request timeout** via `http.TimeoutHandler` (`--timeout`, default
  30s). `ReadHeaderTimeout: 5s` and `IdleTimeout: 60s` on the server.
- **Same dispatch as MCP** (`mcp.CallTool`), so the SDK's strict
  `additionalProperties: false` enforcement applies. Unknown keys are
  rejected with 400.
- **All routes are read-only.** No write surface; no CSRF token needed.
- **Pinned error envelope** (`{error: {code, message}}` with stable codes:
  `bad_request`, `unauthorized`, `unsupported_media_type`,
  `method_not_allowed`, `host_not_permitted`, `timeout`). Asserted by
  `server_test.go::TestHTTPErrorEnvelopeIsPinned`.
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
| `invalid character … looking for value`        | Host is sending non-JSON (or a stale Content-Length framing); aha speaks NDJSON. |
| `unknown tool: refresh` (or similar) over MCP | Write tools are intentionally not exposed; use the CLI directly. |
| Dashboard returns 421 Misdirected Request     | Host header doesn't match the loopback allowlist; use `localhost`, `127.0.0.1`, or set `--allowed-hosts`. |
| Dashboard returns 415 Unsupported Media Type  | POST body sent without `Content-Type: application/json`.        |

## Deliberately deferred

These were considered and consciously left out, with rationale, so the
omission is a decision rather than an oversight:

- **Mid-call context cancellation.** The MCP server does not thread a
  `context.Context` into `search.Query`/`corpus.Read*`/etc., and does not
  honor `notifications/cancelled`. Those functions take a `*sql.DB` and run
  fast local SQLite queries; plumbing cancellation through stable, well-tested
  query code buys little for the local workload and is not worth the churn
  until a real long-running tool exists.
- **`structuredContent` on `tools/call` results for list-typed tools.**
  Object-typed tools (`status`, `verify`, `corpus_size`, `doctor`) emit
  `structuredContent` alongside `content[].text` per the 2025-06-18 spec
  — the six-leg conformance suite verifies the dict-form matches the
  text payload across all three SDKs. List-typed tools (`search`, `read`,
  `clusters`, `conflicts`) omit `structuredContent` because the official Python SDK
  models the field as `Dict[str, Any]` and raises a Pydantic validation
  error on arrays. The typed payload travels in `content[].text` for
  those tools and the TS client surface JSON-parses it transparently.

## Decided

- **`doctor` will not accept a `depot` arg.** The MCP surface stays
  strictly local-only. The CLI's `aha doctor --depot` covers remote
  probing for users who want it; mixing remote network access into the
  MCP path complicates the trust model with no offsetting benefit (an
  agent can shell out to `aha doctor --depot` directly if it needs the
  data).
- **`read` will not gain a `format: "md"` arg.** The typed `[]ReadEntry`
  surface is already exactly what the TS code-mode client needs and what
  the CLI's `aha read --md` renders from. A second wire shape for the
  same payload increases the conformance matrix without unlocking any
  new pattern. Callers that want markdown can render from the typed
  entries client-side.

(Earlier revisions listed `structuredContent` and `readOnlyHint`
annotations as deferred. Both shipped: every tool advertises the typed
`ToolAnnotations{ReadOnlyHint: true, ...}` struct, and object-typed
tools emit `structuredContent` alongside the text content. List-typed
tools omit `structuredContent` because the official Python SDK rejects
array structured content as a Pydantic type error; the typed payload
travels in `content[].text` for those tools.)
