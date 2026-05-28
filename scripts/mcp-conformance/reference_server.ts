// Reference MCP server (official TypeScript SDK) used to validate aha's
// stdio Transport.
//
// Sibling of scripts/mcp-conformance/reference_server.py — same three-tool
// surface (echo / add / fail), implemented with @modelcontextprotocol/sdk's
// McpServer + StdioServerTransport so our connectStdio() can be tested
// against a known-good TypeScript reference.
//
// Run directly:  node --experimental-strip-types reference_server.ts

import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { z } from "zod";

const server = new McpServer({ name: "reference-mcp-ts", version: "0.1.0" });

server.registerTool(
  "echo",
  {
    description: "Echo the input text. Used by the TS client harness to round-trip a payload.",
    inputSchema: { text: z.string() },
  },
  async ({ text }) => ({ content: [{ type: "text", text }] }),
);

server.registerTool(
  "add",
  {
    description: "Return a + b. Exercises typed numeric arguments.",
    inputSchema: { a: z.number(), b: z.number() },
  },
  async ({ a, b }) => ({ content: [{ type: "text", text: String(a + b) }] }),
);

server.registerTool(
  "fail",
  {
    description: "Always raise. Exercises error propagation back through the transport.",
    inputSchema: {},
  },
  async () => {
    throw new Error("intentional");
  },
);

const transport = new StdioServerTransport();
await server.connect(transport);
