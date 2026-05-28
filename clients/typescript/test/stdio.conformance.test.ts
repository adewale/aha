// Conformance test: aha's stdio Transport → official MCP Python SDK server.
//
// Spawns scripts/mcp-conformance/reference_server.py (which uses
// FastMCP), connects our connectStdio() to it, and round-trips three
// tool calls. A successful run proves that our stdio framing speaks
// NDJSON the way the spec demands — *and* the way the canonical
// reference server emits/expects it.
//
// Skipped when AHA_REF_SERVER isn't set, so `node --test` runs in
// environments without Python don't try to exec a missing interpreter.
//
// Run via scripts/verify.sh ts (or directly):
//   AHA_REF_SERVER="python3 $PWD/scripts/mcp-conformance/reference_server.py" \
//     node --experimental-strip-types --test \
//       clients/typescript/test/stdio.conformance.test.ts

import { test } from "node:test";
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { connectStdio } from "../transports/stdio.ts";

const ref = process.env.AHA_REF_SERVER;

test("aha stdio transport against official MCP SDK reference server", { skip: !ref }, async () => {
  const [cmd, ...args] = (ref ?? "").split(" ").filter(Boolean);
  const child = spawn(cmd, args, { stdio: ["pipe", "pipe", "pipe"] });
  child.stderr.on("data", () => {}); // FastMCP logs to stderr; drain quietly

  try {
    const transport = await connectStdio(child.stdin, child.stdout);

    // echo: typed string round-trip
    const echoed = await transport.call("echo", { text: "ndjson hello" }) as string;
    assert.equal(echoed, "ndjson hello");

    // add: typed numeric args
    const sum = await transport.call("add", { a: 21, b: 21 }) as number;
    assert.equal(sum, 42);

    // fail: error must propagate
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
});
