// Runtime tests for the stdio transport.
//
// Run with Node 22+ (type stripping):
//   node --experimental-strip-types --test clients/typescript/test/stdio.test.ts
//
// The first suite drives connectStdio against in-memory fake streams: it
// proves the framing, the initialize handshake, request/response correlation,
// and partial-chunk reassembly without needing a built binary.
//
// The second suite spawns a real `aha mcp` and is skipped unless AHA_BIN and
// AHA_CONFIG point at a binary and a populated corpus.

import { test } from "node:test";
import assert from "node:assert/strict";
import { connectStdio, AhaMcpError } from "../transports/stdio.ts";
import { connectHTTP } from "../transports/http.ts";
import { aha, parseRef, formatRef, type Ref } from "../aha-mcp.ts";

const encoder = new TextEncoder();
const decoder = new TextDecoder();

// NDJSON: one JSON object per line, terminated by '\n'. Matches the MCP
// stdio transport spec ("Messages are delimited by newlines, and MUST NOT
// contain embedded newlines.")
function frame(obj: unknown): Uint8Array {
  return encoder.encode(JSON.stringify(obj) + "\n");
}

// parseFrames mirrors the wire format so the test can read what the client
// wrote to stdin, independent of the module under test.
function parseFrames(buf: Uint8Array): unknown[] {
  const messages: unknown[] = [];
  let rest = buf;
  for (;;) {
    let nl = -1;
    for (let i = 0; i < rest.length; i++) {
      if (rest[i] === 10) { nl = i; break; }
    }
    if (nl < 0) return messages;
    let line = rest.subarray(0, nl);
    if (line.length > 0 && line[line.length - 1] === 13) {
      line = line.subarray(0, line.length - 1);
    }
    rest = rest.subarray(nl + 1);
    const text = decoder.decode(line).trim();
    if (text.length === 0) continue;
    messages.push(JSON.parse(text));
  }
}

// FakeStreams is a controllable stdin/stdout pair. Writes to stdin are
// captured; stdout frames are pushed via emitData().
const compatibleCapabilities = {
  schema: "aha.mcp.v2",
  http_schema: "aha.http.v2",
  required_features: ["read-only-v1", "strict-input-v1", "structured-errors-v1"],
  tools: ["aha_capabilities", "analyse_failure_trajectory", "analyse_failures", "overview", "search", "show", "status", "workspace_conflicts", "workspace_size", "workspace_verify"],
};

class FakeStreams {
  written: Uint8Array = new Uint8Array(0);
  private dataCb: ((c: Uint8Array) => void) | null = null;

  stdin = {
    write: (chunk: Uint8Array | string) => {
      const bytes = typeof chunk === "string" ? encoder.encode(chunk) : chunk;
      const out = new Uint8Array(this.written.length + bytes.length);
      out.set(this.written, 0);
      out.set(bytes, this.written.length);
      this.written = out;
      return true;
    },
  };

  stdout = {
    on: (event: string, listener: (...args: unknown[]) => void) => {
      if (event === "data") this.dataCb = listener as (c: Uint8Array) => void;
      return this.stdout;
    },
  };

  emitData(chunk: Uint8Array) {
    this.dataCb?.(chunk);
  }

  // requests returns every JSON-RPC object the client has written so far.
  requests(): { id?: number; method: string; params?: Record<string, unknown> }[] {
    return parseFrames(this.written) as { id?: number; method: string; params?: Record<string, unknown> }[];
  }
}

async function answerStdioCapabilities(fake: FakeStreams, value: unknown = compatibleCapabilities, listedNames?: string[]) {
  await Promise.resolve();
  const list = fake.requests().find((m) => m.method === "tools/list");
  assert.ok(list, "client did not request tools/list");
  const advertised = (value as { tools?: unknown })?.tools;
  const names = listedNames ?? (Array.isArray(advertised) ? advertised.filter((name): name is string => typeof name === "string") : []);
  fake.emitData(frame({ jsonrpc: "2.0", id: list!.id, result: { tools: names.map((name) => ({ name })) } }));
  await Promise.resolve();
  const call = fake.requests().find((m) => m.method === "tools/call" && (m.params as { name?: string })?.name === "aha_capabilities");
  assert.ok(call, "client did not negotiate aha_capabilities");
  fake.emitData(frame({ jsonrpc: "2.0", id: call!.id, result: { content: [{ type: "text", text: JSON.stringify(value) }] } }));
}

test("handshake, dispatch, and result correlation", async () => {
  const fake = new FakeStreams();
  const connecting = connectStdio(fake.stdin, fake.stdout);

  // The client should have sent initialize; respond to its id.
  const init = fake.requests().find((m) => m.method === "initialize");
  assert.ok(init, "client did not send initialize");
  fake.emitData(frame({ jsonrpc: "2.0", id: init!.id, result: { protocolVersion: "2024-11-05" } }));
  await answerStdioCapabilities(fake);

  const transport = await connecting;
  const tools = aha(transport);

  // Client should have sent the initialized notification (no id).
  assert.ok(
    fake.requests().some((m) => m.method === "notifications/initialized"),
    "client did not send initialized notification",
  );

  const pending = tools.search({ query: "needle", limit: 5 });
  const call = fake.requests().find((m) => m.method === "tools/call" && (m.params as { name?: string })?.name === "search");
  assert.ok(call, "client did not send search tools/call");

  const payload = [{ ref_text: "msg:v1:abc", role: "user", snippet: "found the needle" }];
  fake.emitData(
    frame({
      jsonrpc: "2.0",
      id: call!.id,
      result: { content: [{ type: "text", text: JSON.stringify(payload) }] },
    }),
  );

  const hits = await pending;
  assert.equal(hits.length, 1);
  assert.equal(hits[0].ref_text, "msg:v1:abc");
});

test("reassembles a response split across two data events", async () => {
  const fake = new FakeStreams();
  const connecting = connectStdio(fake.stdin, fake.stdout);
  const init = fake.requests().find((m) => m.method === "initialize")!;

  const full = frame({ jsonrpc: "2.0", id: init.id, result: {} });
  const cut = Math.floor(full.length / 2);
  fake.emitData(full.subarray(0, cut)); // partial header/body
  fake.emitData(full.subarray(cut)); // remainder completes the frame
  await answerStdioCapabilities(fake);

  const transport = await connecting; // resolves only if the split frame parsed
  assert.ok(transport);
});

test("stdio rejects unsupported required compatibility features", async () => {
  const fake = new FakeStreams();
  const connecting = connectStdio(fake.stdin, fake.stdout);
  const init = fake.requests().find((m) => m.method === "initialize")!;
  fake.emitData(frame({ jsonrpc: "2.0", id: init.id, result: {} }));
  await answerStdioCapabilities(fake, { ...compatibleCapabilities, required_features: ["future-v9"] });
  await assert.rejects(connecting, /unsupported feature/);
});

test("stdio rejects a server missing generated tools", async () => {
  const fake = new FakeStreams();
  const connecting = connectStdio(fake.stdin, fake.stdout);
  const init = fake.requests().find((m) => m.method === "initialize")!;
  fake.emitData(frame({ jsonrpc: "2.0", id: init.id, result: {} }));
  await answerStdioCapabilities(fake, { ...compatibleCapabilities, tools: compatibleCapabilities.tools.filter((tool) => tool !== "search") });
  await assert.rejects(connecting, /does not provide search/);
});

test("stdio rejects tools advertised but absent from tools/list", async () => {
  const fake = new FakeStreams();
  const connecting = connectStdio(fake.stdin, fake.stdout);
  const init = fake.requests().find((m) => m.method === "initialize")!;
  fake.emitData(frame({ jsonrpc: "2.0", id: init.id, result: {} }));
  await answerStdioCapabilities(fake, compatibleCapabilities, compatibleCapabilities.tools.filter((tool) => tool !== "search"));
  await assert.rejects(connecting, /falsely advertises search/);
});

test("surfaces JSON-RPC errors as rejections", async () => {
  const fake = new FakeStreams();
  const connecting = connectStdio(fake.stdin, fake.stdout);
  const init = fake.requests().find((m) => m.method === "initialize")!;
  fake.emitData(frame({ jsonrpc: "2.0", id: init.id, result: {} }));
  await answerStdioCapabilities(fake);
  const tools = aha(await connecting);

  const pending = tools.show({ ref: "bad" });
  const call = fake.requests().find((m) => m.method === "tools/call" && (m.params as { name?: string })?.name === "show")!;
  fake.emitData(frame({ jsonrpc: "2.0", id: call.id, error: { code: -32000, message: "invalid ref" } }));

  await assert.rejects(pending, /invalid ref/);
});

test("JSON-RPC errors surface as AhaMcpError with the wire code", async () => {
  const fake = new FakeStreams();
  const connecting = connectStdio(fake.stdin, fake.stdout);
  const init = fake.requests().find((m) => m.method === "initialize")!;
  fake.emitData(frame({ jsonrpc: "2.0", id: init.id, result: {} }));
  await answerStdioCapabilities(fake);
  const tools = aha(await connecting);

  const pending = tools.show({ ref: "bad" });
  const call = fake.requests().find((m) => m.method === "tools/call" && (m.params as { name?: string })?.name === "show")!;
  fake.emitData(frame({ jsonrpc: "2.0", id: call.id, error: { code: -32000, message: "invalid ref" } }));

  try {
    await pending;
    assert.fail("expected throw");
  } catch (e) {
    assert.ok(e instanceof AhaMcpError, `expected AhaMcpError, got ${typeof e}`);
    assert.equal((e as AhaMcpError).code, "-32000");
    assert.match((e as AhaMcpError).message, /invalid ref/);
  }
});

test("HTTP transport routes typed failure-analysis and overview tools", async () => {
  const calls: { url: string; init: RequestInit }[] = [];
  const fakeFetch = async (url: string | URL | Request, init?: RequestInit): Promise<Response> => {
    calls.push({ url: String(url), init: init ?? {} });
    const body = String(url).endsWith("/api/v2/capabilities")
      ? JSON.stringify(compatibleCapabilities)
      : String(url).endsWith("/api/v2/overview") ? "{}" : "[]";
    return new Response(body, { status: 200, headers: { "Content-Type": "application/json" } });
  };
  const tools = aha(connectHTTP("http://127.0.0.1:18428/", { fetch: fakeFetch as typeof fetch }));
  await tools.analyse_failures({ limit: 1, state: "unresolved" });
  await tools.analyse_failure_trajectory({ ref: "msg:v1:c2s:ZTE", ordinal: 0 });
  await tools.overview();
  assert.equal(calls.length, 4);
  assert.equal(calls[0].url, "http://127.0.0.1:18428/api/v2/capabilities");
  assert.equal(calls[0].init.method, "GET");
  assert.equal(calls[1].url, "http://127.0.0.1:18428/api/v2/analyse/failures");
  assert.equal(calls[1].init.method, "POST");
  assert.equal((calls[1].init.headers as Record<string, string>)["Content-Type"], "application/json");
  assert.equal((calls[1].init.headers as Record<string, string>)["Aha-HTTP-Contract"], "aha.http.v2");
  assert.equal(calls[1].init.body, JSON.stringify({ limit: 1, state: "unresolved" }));
  assert.equal(calls[2].url, "http://127.0.0.1:18428/api/v2/analyse/failure-trajectory");
  assert.equal(calls[2].init.method, "POST");
  assert.equal(calls[2].init.body, JSON.stringify({ ref: "msg:v1:c2s:ZTE", ordinal: 0 }));
  assert.equal(calls[3].url, "http://127.0.0.1:18428/api/v2/overview");
  assert.equal(calls[3].init.method, "GET");
});

test("HTTP conflicts transport sends explicit bounded pagination", async () => {
  const calls: { url: string; init: RequestInit }[] = [];
  const fakeFetch = async (url: string | URL | Request, init?: RequestInit): Promise<Response> => {
    calls.push({ url: String(url), init: init ?? {} });
    const body = String(url).endsWith("/capabilities") ? compatibleCapabilities : [];
    return new Response(JSON.stringify(body), { status: 200 });
  };
  const tools = aha(connectHTTP("http://127.0.0.1:18428", { fetch: fakeFetch as typeof fetch }));
  await tools.workspace_conflicts({ limit: 50, offset: 200 });
  assert.equal(calls[1].url, "http://127.0.0.1:18428/api/v2/workspace/conflicts");
  assert.equal(calls[1].init.method, "POST");
  assert.equal(calls[1].init.body, JSON.stringify({ limit: 50, offset: 200 }));
});

test("HTTP rejects incompatible capability negotiation before tool calls", async () => {
  const calls: string[] = [];
  const fakeFetch = async (url: string | URL | Request): Promise<Response> => {
    calls.push(String(url));
    return new Response(JSON.stringify({ ...compatibleCapabilities, http_schema: "aha.http.v99" }), { status: 200 });
  };
  const tools = aha(connectHTTP("http://127.0.0.1:18428", { fetch: fakeFetch as typeof fetch }));
  await assert.rejects(tools.status(), /compatibility_required/);
  assert.deepEqual(calls, ["http://127.0.0.1:18428/api/v2/capabilities"]);
});

test("HTTP rejects a server missing generated tools", async () => {
  const fakeFetch = async (): Promise<Response> => new Response(JSON.stringify({ ...compatibleCapabilities, tools: compatibleCapabilities.tools.filter((tool) => tool !== "status") }), { status: 200 });
  const tools = aha(connectHTTP("http://127.0.0.1:18428", { fetch: fakeFetch as typeof fetch }));
  await assert.rejects(tools.status(), /does not provide status/);
});

test("parseRef + formatRef round-trip every canonical shape", () => {
  const cases: Ref[] = [
    { kind: "message", session_key: "sk1_abc", entry_id: "entry:with/slash" },
    { kind: "session", session_key: "sk1_abc" },
    { kind: "artifact", artifact_sha256: "a".repeat(64) },
  ];
  assert.equal(formatRef(cases[0]), "msg:v1:c2sxX2FiYw:ZW50cnk6d2l0aC9zbGFzaA");
  assert.equal(formatRef(cases[1]), "session:v1:c2sxX2FiYw");
  for (const ref of cases) {
    const wire = formatRef(ref);
    const parsed = parseRef(wire);
    assert.deepEqual(parsed, ref, `round-trip for ${ref.kind} failed: ${wire}`);
  }
});

test("parseRef returns null for malformed input", () => {
  for (const bad of ["", "msg:abc", "msg:v1:sk1_abc:p1", "session:v1:sk1_abc", "session:v2:sk", "artifact:v1:not-a-sha", "garbage"]) {
    assert.equal(parseRef(bad), null, `expected null for ${JSON.stringify(bad)}`);
  }
});
