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
import { assertAttestedConformance } from "./attestation.ts";

const { ahaBin, ahaConfig } = assertAttestedConformance();

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

  // ---- 5. error-path: empty search must return [], not throw ----
  // The canonical pattern relies on filter/map over the search result, so
  // an empty list returned from a query that matches nothing has to be a
  // typed empty array — not null, not an exception.
  const noHits: SearchResult[] = await tools.search({ query: "definitelynotinthecorpus" });
  if (!Array.isArray(noHits) || noHits.length !== 0) {
    throw new Error(`empty search should return []: ${JSON.stringify(noHits).slice(0, 100)}`);
  }
  // Filter+map over the empty list must not throw — a real agent
  // would write this composition unconditionally.
  const filteredEmpty = noHits.filter((h) => h.role === "user").map((h) => h.ref_text);
  if (filteredEmpty.length !== 0) throw new Error("filter+map over empty list produced entries");
  console.log("empty-search composition OK: [] flows through filter+map without throwing");

  // ---- 6. error-path: invalid ref propagates as a typed error ----
  // Promise.all over reads must reject if any leg fails, so the agent's
  // code can either catch it or surface it. A bad ref is the simplest
  // way to force one leg to error.
  let badRefThrew = false;
  try {
    await tools.read({ ref: "msg:v1:not-a-real-ref", before: 1, after: 1 });
  } catch {
    badRefThrew = true;
  }
  if (!badRefThrew) throw new Error("tools.read with invalid ref should have thrown");
  console.log("invalid-ref read OK: typed surface propagates the error");

  // ---- 7. status (no-arg tool) ----
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
