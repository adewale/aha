// Conformance tests: aha's stdio Transport against known-good MCP servers.
//
// Two scenarios prove the same wire-format claim from independent angles:
//   1. AHA_REF_SERVER → a Python FastMCP reference (official Python SDK)
//   2. AHA_REF_SERVER_TS → a TypeScript McpServer reference (official TS SDK)
//
// Each is skipped if its env var isn't set, so this file can run in
// Python-only, TS-only, or no-reference-server environments without
// failing.
//
// Run via `scripts/verify.sh mcp`, or directly:
//   AHA_REF_SERVER="python3 .../reference_server.py" \
//   AHA_REF_SERVER_TS="node --experimental-strip-types .../reference_server.ts" \
//     node --experimental-strip-types --test \
//       clients/typescript/test/stdio.conformance.test.ts

import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { connectStdio } from "../transports/stdio.ts";

async function roundTripThreeTools(refCmd: string) {
  const [cmd, ...args] = refCmd.split(" ").filter(Boolean);
  const child = spawn(cmd, args, { stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.on("data", () => {}); // drain server logs

  try {
    const transport = await connectStdio(child.stdin, child.stdout);

    const echoed = (await transport.call("echo", { text: "ndjson hello" })) as string;
    assert.equal(echoed, "ndjson hello");

    const sum = (await transport.call("add", { a: 21, b: 21 })) as number;
    assert.equal(sum, 42);

    let threw = false;
    try {
      await transport.call("fail", {});
    } catch {
      threw = true;
    }
    assert.equal(threw, true, "fail tool should propagate an error");
  } finally {
    child.stdin.end();
    child.kill();
  }
}

const refPy = process.env.AHA_REF_SERVER;
const refTs = process.env.AHA_REF_SERVER_TS;
const refGo = process.env.AHA_REF_SERVER_GO;

test(
  "aha stdio transport against official Python SDK FastMCP reference",
  { skip: !refPy },
  async () => roundTripThreeTools(refPy ?? ""),
);

test(
  "aha stdio transport against official TypeScript SDK McpServer reference",
  { skip: !refTs },
  async () => roundTripThreeTools(refTs ?? ""),
);

test(
  "aha stdio transport against official Go SDK reference",
  { skip: !refGo },
  async () => roundTripThreeTools(refGo ?? ""),
);
