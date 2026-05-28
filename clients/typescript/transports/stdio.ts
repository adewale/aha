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
// This file is hand-written (the typed surface is generated; the runtime
// adapter is small enough to keep readable).

import type { Transport } from "../aha-mcp.js";

interface Writable { write(chunk: Buffer | string): void; }
interface Readable {
  on(event: "data", listener: (chunk: Buffer) => void): this;
  on(event: "end", listener: () => void): this;
  on(event: "error", listener: (err: Error) => void): this;
}

interface PendingCall {
  resolve: (value: unknown) => void;
  reject: (err: Error) => void;
}

let nextId = 1;

function encodeFrame(message: unknown): Buffer {
  const body = Buffer.from(JSON.stringify(message), "utf8");
  const header = Buffer.from(`Content-Length: ${body.length}\r\n\r\n`, "ascii");
  return Buffer.concat([header, body]);
}

function parseFrames(buf: Buffer): { messages: unknown[]; rest: Buffer } {
  const messages: unknown[] = [];
  let rest = buf;
  for (;;) {
    const sep = rest.indexOf("\r\n\r\n");
    if (sep < 0) return { messages, rest };
    const header = rest.slice(0, sep).toString("ascii");
    const match = header.match(/Content-Length:\s*(\d+)/i);
    if (!match) throw new Error("missing Content-Length header");
    const length = Number(match[1]);
    const start = sep + 4;
    const end = start + length;
    if (rest.length < end) return { messages, rest };
    messages.push(JSON.parse(rest.slice(start, end).toString("utf8")));
    rest = rest.slice(end);
  }
}

export interface StdioConnectOptions {
  initializeTimeoutMs?: number;
}

// connectStdio attaches a Transport to the given stdin/stdout streams and
// performs the MCP initialize handshake before resolving. Reject if
// initialize doesn't respond inside initializeTimeoutMs (default 5000).
export function connectStdio(
  stdin: Writable,
  stdout: Readable,
  opts: StdioConnectOptions = {},
): Promise<Transport> {
  const pending = new Map<number, PendingCall>();
  let buffer = Buffer.alloc(0);

  stdout.on("data", (chunk) => {
    buffer = Buffer.concat([buffer, chunk]);
    try {
      const { messages, rest } = parseFrames(buffer);
      buffer = rest;
      for (const msg of messages) {
        const m = msg as { id?: number; result?: unknown; error?: { code: number; message: string } };
        if (typeof m.id !== "number") continue; // notifications
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
      // Rejecting all pending calls is the only safe move on framing error.
      for (const [, p] of pending) p.reject(e as Error);
      pending.clear();
    }
  });

  stdout.on("end", () => {
    for (const [, p] of pending) p.reject(new Error("aha MCP stdio closed"));
    pending.clear();
  });
  stdout.on("error", (err) => {
    for (const [, p] of pending) p.reject(err);
    pending.clear();
  });

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
    rawCall("initialize", { protocolVersion: "2024-11-05", capabilities: {}, clientInfo: { name: "aha-stdio", version: "0.1.0" } })
      .then(() => {
        clearTimeout(timer);
        // Best-effort notification per MCP spec; no response expected.
        stdin.write(encodeFrame({ jsonrpc: "2.0", method: "notifications/initialized" }));
        const transport: Transport = {
          async call(name, args) {
            const result = await rawCall("tools/call", { name, arguments: args }) as {
              content: { type: string; text: string }[];
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
