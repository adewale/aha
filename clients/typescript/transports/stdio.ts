// Stdio Transport for the aha MCP server.
//
// Spawns `aha mcp` as a child process and speaks JSON-RPC 2.0 over its
// stdin/stdout using the newline-delimited framing required by the MCP
// stdio transport
// (https://modelcontextprotocol.io/specification/2025-06-18/basic/transports):
//
//   "Messages are delimited by newlines, and MUST NOT contain embedded newlines."
//
// Use this when you want to call aha from a Node-based agent runtime or test.
//
// Example:
//   import { spawn } from "node:child_process";
//   import { aha } from "../aha-mcp.js";
//   import { connectStdio } from "./stdio.js";
//
//   const child = spawn("aha", ["mcp"]);
//   const transport = await connectStdio(child.stdin, child.stdout);
//   const tools = aha(transport);
//   const hits = await tools.search({ query: "migration", limit: 50 });
//   console.log(hits.map(h => h.ref_text));
//
// Framing uses web-standard Uint8Array/TextEncoder rather than Node Buffer so
// the same code compiles and runs in non-Node sandboxes. This file is
// hand-written (the typed surface is generated; the runtime adapter is small
// enough to keep readable).

import type { Transport } from "../aha-mcp.js";

/**
 * AhaMcpError surfaces a tool-side or transport-side failure with a
 * stable `code` string callers can match on:
 *   - JSON-RPC error codes from the wire become strings like `"-32000"`
 *   - Tool-level isError results use the synthetic code `"tool_error"`
 *   - Framing / connection failures use `"transport_closed"`
 *
 * Round-tripped via the typed surface so callers can `instanceof
 * AhaMcpError` and branch on `.code` instead of regex-matching message
 * text. Backwards-compatible with `try { ... } catch (e: unknown)`
 * patterns that look at `.message`.
 */
export class AhaMcpError extends Error {
  readonly code: string;
  readonly data?: unknown;
  constructor(message: string, code: string, data?: unknown) {
    super(message);
    this.name = "AhaMcpError";
    this.code = code;
    if (data !== undefined) this.data = data;
  }
}

// Minimal structural views of a child process's stdio streams. A Node
// ChildProcess's stdin/stdout satisfy these without importing @types/node.
interface Writable {
  write(chunk: Uint8Array | string): unknown;
}
interface Readable {
  on(event: "data", listener: (chunk: Uint8Array) => void): unknown;
  on(event: "end", listener: () => void): unknown;
  on(event: "error", listener: (err: Error) => void): unknown;
}

interface PendingCall {
  resolve: (value: unknown) => void;
  reject: (err: Error) => void;
}

// Bytes pins the TypedArray's backing-buffer generic to ArrayBufferLike so
// values produced by `new Uint8Array`, `.subarray`, and stream `data` events
// all unify (TS 6.0 made the buffer type parameter explicit).
type Bytes = Uint8Array<ArrayBufferLike>;

const encoder = new TextEncoder();
const decoder = new TextDecoder();

function concat(a: Bytes, b: Bytes): Bytes {
  const out = new Uint8Array(a.length + b.length);
  out.set(a, 0);
  out.set(b, a.length);
  return out;
}

// indexOfNewline returns the offset of the next '\n' in buf, or -1.
function indexOfNewline(buf: Bytes): number {
  for (let i = 0; i < buf.length; i++) {
    if (buf[i] === 10) return i;
  }
  return -1;
}

// encodeFrame produces a compact JSON encoding of `message` followed by a
// single '\n'. JSON.stringify never emits literal newlines, so the
// spec-mandated "MUST NOT contain embedded newlines" rule holds by
// construction.
function encodeFrame(message: unknown): Bytes {
  const json = JSON.stringify(message);
  if (json.includes("\n") || json.includes("\r")) {
    throw new Error("encoded JSON contains a newline");
  }
  return encoder.encode(json + "\n");
}

// parseFrames splits the buffer on '\n' boundaries, decodes each non-empty
// line as JSON, and returns the still-buffered tail (data after the last
// newline). Tolerates '\r\n' line endings and skips blank lines, mirroring
// the Go side.
function parseFrames(buf: Bytes): { messages: unknown[]; rest: Bytes } {
  const messages: unknown[] = [];
  let rest = buf;
  for (;;) {
    const nl = indexOfNewline(rest);
    if (nl < 0) return { messages, rest };
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

export interface StdioConnectOptions {
  initializeTimeoutMs?: number;
}

// connectStdio attaches a Transport to the given stdin/stdout streams and
// performs the MCP initialize handshake before resolving. Rejects if
// initialize doesn't respond inside initializeTimeoutMs (default 5000).
export function connectStdio(
  stdin: Writable,
  stdout: Readable,
  opts: StdioConnectOptions = {},
): Promise<Transport> {
  const pending = new Map<number, PendingCall>();
  let nextId = 1;
  let buffer: Bytes = new Uint8Array(0);

  const rejectAll = (err: Error) => {
    for (const [, p] of pending) p.reject(err);
    pending.clear();
  };

  stdout.on("data", (chunk: Uint8Array) => {
    buffer = concat(buffer, chunk);
    try {
      const { messages, rest } = parseFrames(buffer);
      buffer = rest;
      for (const msg of messages) {
        const m = msg as { id?: number; result?: unknown; error?: { code: number; message: string } };
        if (typeof m.id !== "number") continue; // notifications carry no id
        const p = pending.get(m.id);
        if (!p) continue;
        pending.delete(m.id);
        if (m.error) {
          p.reject(new AhaMcpError(m.error.message, String(m.error.code)));
        } else {
          p.resolve(m.result);
        }
      }
    } catch (e) {
      rejectAll(e as Error);
    }
  });

  stdout.on("end", () => rejectAll(new AhaMcpError("aha MCP stdio closed", "transport_closed")));
  stdout.on("error", (err: Error) => rejectAll(err));

  function rawCall(method: string, params?: Record<string, unknown>): Promise<unknown> {
    return new Promise((resolve, reject) => {
      const id = nextId++;
      pending.set(id, { resolve, reject });
      stdin.write(encodeFrame({ jsonrpc: "2.0", id, method, params }));
    });
  }

  const initTimeout = opts.initializeTimeoutMs ?? 5000;
  return new Promise<Transport>((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("initialize timed out")), initTimeout);
    rawCall("initialize", {
      protocolVersion: "2024-11-05",
      capabilities: {},
      clientInfo: { name: "aha-stdio", version: "0.1.0" },
    })
      .then(() => {
        clearTimeout(timer);
        // Best-effort notification per MCP spec; no response expected.
        stdin.write(encodeFrame({ jsonrpc: "2.0", method: "notifications/initialized" }));
        const transport: Transport = {
          async call(name, args) {
            const result = (await rawCall("tools/call", { name, arguments: args })) as {
              content?: { type: string; text: string }[];
              structuredContent?: unknown;
              isError?: boolean;
            };
            if (result?.isError) {
              const text = result?.content?.[0]?.text ?? "tool error";
              throw new AhaMcpError(text, "tool_error");
            }
            // Per the 2025-06-18 spec, a server that emits structuredContent
            // SHOULD also emit the serialized form in content[].text. So
            // text is the universal channel — every conformant server has
            // it, and its shape is whatever the tool returned. We use that
            // exclusively; structuredContent stays on the wire as
            // informational metadata but isn't read here (different servers
            // wrap primitives differently: FastMCP emits {result: ...},
            // aha emits the value directly).
            const text = result?.content?.[0]?.text;
            if (typeof text !== "string") {
              throw new Error("tools/call returned no text content");
            }
            // Most servers JSON-encode the payload; FastMCP does too for
            // primitives ('"hi"' rather than 'hi'). Try to parse; fall back
            // to the raw string for the rare server that emits plain text.
            try {
              return JSON.parse(text);
            } catch {
              return text;
            }
          },
        };
        resolve(transport);
      })
      .catch((err) => {
        clearTimeout(timer);
        reject(err);
      });
  });
}
