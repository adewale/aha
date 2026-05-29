// Code Mode workflow conformance: exercise the typed aha surface the way a
// code-mode runtime would.
//
// Cloudflare codemode and Anthropic code-execution-with-MCP both turn an
// MCP server's tools into a typed function surface that the agent drives
// with TypeScript. The canonical pattern is fan-out + fan-in:
//
//     const hits = await tools.search({ ... });            // 1 round-trip
//     const refs = hits.filter(...).map(h => h.ref_text);  // local logic
//     const ctx  = await Promise.all(refs.map(r =>          // N parallel
//                    tools.read({ ref: r, before, after })));
//
// This script proves that pattern works end-to-end against aha mcp via
// our typed `aha-mcp.ts` surface and `connectStdio` transport:
//
//   1. spawn `aha mcp` as a child process;
//   2. construct the typed `aha()` helper over the stdio Transport;
//   3. run the canonical fan-out: search hits → filter user-role → read
//      each survivor in parallel;
//   4. assert each step yields a typed value matching the surface contract.
//
// A green run means a code-mode runtime can ship the same TS without
// modification.

import { spawn } from "node:child_process";
import { aha, type SearchResult, type ReadEntry } from "../../clients/typescript/aha-mcp.ts";
import { connectStdio } from "../../clients/typescript/transports/stdio.ts";

const ahaBin = process.env.AHA_BIN ?? "/tmp/aha";
const ahaConfig = process.env.AHA_CONFIG;

if (!ahaConfig) {
  console.error("AHA_CONFIG unset; skipping");
  process.exit(77);
}

const child = spawn(ahaBin, ["mcp", "--config", ahaConfig], {
  stdio: ["pipe", "pipe", "pipe"],
});
child.stderr.on("data", () => {}); // drain server diagnostics

async function main() {
  // ---- 1. typed surface construction ----
  const transport = await connectStdio(child.stdin, child.stdout);
  const tools = aha(transport);

  // ---- 2. canonical fan-out: search ----
  const hits: SearchResult[] = await tools.search({ query: "hello", limit: 20 });
  if (!Array.isArray(hits)) throw new Error(`search did not return a list: ${typeof hits}`);
  if (hits.length === 0) throw new Error("search returned no hits against the fixture corpus");

  // Typed-field access should compile and succeed at runtime.
  const sample = hits[0];
  for (const field of ["ref_text", "session_key", "role", "snippet"] as const) {
    if (!(field in sample)) throw new Error(`SearchResult missing typed field: ${String(field)}`);
  }
  console.log(`search OK: ${hits.length} hit(s)`);

  // ---- 3. local logic between calls ----
  const userRefs = hits.filter((h) => h.role === "user" || h.role === "").map((h) => h.ref_text);
  const refs = userRefs.length > 0 ? userRefs : hits.map((h) => h.ref_text);
  console.log(`filter OK: kept ${refs.length} of ${hits.length} refs after role filter`);

  // ---- 4. fan-out: parallel read ----
  const contexts: ReadEntry[][] = await Promise.all(
    refs.slice(0, 5).map((ref) => tools.read({ ref, before: 1, after: 3 })),
  );
  if (contexts.length === 0) throw new Error("no contexts returned");
  for (const ctx of contexts) {
    if (!Array.isArray(ctx) || ctx.length === 0) {
      throw new Error(`read returned non-list or empty: ${JSON.stringify(ctx)}`);
    }
    for (const field of ["entry_id", "timestamp", "role", "text"] as const) {
      if (!(field in ctx[0])) throw new Error(`ReadEntry missing typed field: ${String(field)}`);
    }
  }
  console.log(`read fan-out OK: ${contexts.length} contexts, ${contexts.reduce((n, c) => n + c.length, 0)} entries total`);

  // ---- 5. status (no-arg tool) ----
  const status = await tools.status();
  if (typeof status !== "object" || status === null) {
    throw new Error(`status not an object: ${typeof status}`);
  }
  const sessions = (status as { sessions?: number }).sessions;
  if (typeof sessions !== "number") {
    throw new Error(`status.sessions not a number: ${JSON.stringify(status).slice(0, 200)}`);
  }
  console.log(`status OK: sessions=${sessions}`);

  console.log("Code Mode workflow conformance OK: aha typed surface drives search→filter→read fan-out end-to-end.");
}

main()
  .then(() => {
    child.stdin.end();
    child.kill();
    process.exit(0);
  })
  .catch((err) => {
    console.error(err);
    child.stdin.end();
    child.kill();
    process.exit(1);
  });
