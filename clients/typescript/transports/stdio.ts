// Stdio Transport for the aha MCP server.
//
// Spawns `aha mcp` as a child process and speaks JSON-RPC 2.0 over its
// stdin/stdout using LSP-style `Content-Length:` framing. Use this when you
// want to call aha from a Node-based agent runtime or test.
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

// indexOfHeaderEnd returns the offset of the "\r\n\r\n" header terminator, or
// -1 if it is not present in buf.
function indexOfHeaderEnd(buf: Bytes): number {
  for (let i = 0; i + 3 < buf.length; i++) {
    if (buf[i] === 13 && buf[i + 1] === 10 && buf[i + 2] === 13 && buf[i + 3] === 10) {
      return i;
    }
  }
  return -1;
}

function encodeFrame(message: unknown): Bytes {
  const body = encoder.encode(JSON.stringify(message));
  const header = encoder.encode(`Content-Length: ${body.length}\r\n\r\n`);
  return concat(header, body);
}

function parseFrames(buf: Bytes): { messages: unknown[]; rest: Bytes } {
  const messages: unknown[] = [];
  let rest = buf;
  for (;;) {
    const sep = indexOfHeaderEnd(rest);
    if (sep < 0) return { messages, rest };
    const header = decoder.decode(rest.subarray(0, sep));
    const match = header.match(/Content-Length:\s*(\d+)/i);
    if (!match) throw new Error("missing Content-Length header");
    const length = Number(match[1]);
    const start = sep + 4;
    const end = start + length;
    if (rest.length < end) return { messages, rest };
    messages.push(JSON.parse(decoder.decode(rest.subarray(start, end))));
    rest = rest.subarray(end);
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
          p.reject(new Error(`aha MCP error ${m.error.code}: ${m.error.message}`));
        } else {
          p.resolve(m.result);
        }
      }
    } catch (e) {
      rejectAll(e as Error);
    }
  });

  stdout.on("end", () => rejectAll(new Error("aha MCP stdio closed")));
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
            };
            const text = result?.content?.[0]?.text;
            if (typeof text !== "string") {
              throw new Error("tools/call returned no text content");
            }
            return JSON.parse(text);
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
