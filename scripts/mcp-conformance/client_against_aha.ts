// Conformance test: official @modelcontextprotocol/sdk Client → aha mcp.
//
// Mirrors scripts/mcp-conformance/client_against_aha.py but uses the
// TypeScript SDK as the validating client. A green run proves aha's
// server is interoperable with both Anthropic-maintained SDKs.
//
// Run via `scripts/verify.sh mcp` (it spawns a fresh corpus first); or
// Direct ambient AHA_BIN/AHA_CONFIG values are rejected; run through the
// attested private workspace created by scripts/verify.sh mcp.

import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { assertAttestedConformance } from "./attestation.ts";

const EXPECTED_TOOLS = [
  "search",
  "read",
  "incidents",
  "incident_trajectory",
  "overview",
  "status",
  "verify",
  "conflicts",
  "corpus_size",
  "doctor",
];

const { ahaBin, ahaConfig } = assertAttestedConformance();

const transport = new StdioClientTransport({
  command: ahaBin,
  args: ["mcp", "serve", "--config", ahaConfig],
});

const client = new Client({ name: "aha-conformance-ts", version: "0.1.0" });

async function main() {
  await client.connect(transport);
  const init = client.getServerVersion();
  if (!init) throw new Error("no serverInfo after connect");
  if (init.name !== "aha") {
    throw new Error(`serverInfo.name=${JSON.stringify(init.name)}, want aha`);
  }
  console.log(`initialize OK: server=${init.name} ${init.version}`);

  // tools/list
  const listed = await client.listTools();
  const got = listed.tools.map((t) => t.name).sort();
  const want = [...EXPECTED_TOOLS].sort();
  if (JSON.stringify(got) !== JSON.stringify(want)) {
    throw new Error(`tools/list mismatch:\n  got:  ${got.join(",")}\n  want: ${want.join(",")}`);
  }
  console.log(`tools/list OK: ${got.join(", ")}`);

  // readOnlyHint must be advertised per 2025-06-18.
  for (const t of listed.tools) {
    if (!t.annotations || t.annotations.readOnlyHint !== true) {
      throw new Error(`${t.name}: readOnlyHint not true (${JSON.stringify(t.annotations)})`);
    }
  }
  console.log(`tool annotations OK: readOnlyHint=true on all ${listed.tools.length} tools`);

  // tools/call status — object-returning tool. structuredContent should
  // be present and match the text payload.
  const statusRes = await client.callTool({ name: "status" });
  if (!Array.isArray(statusRes.content) || statusRes.content.length === 0) {
    throw new Error("status: no content blocks");
  }
  const block = statusRes.content[0] as { type: string; text: string };
  const status = JSON.parse(block.text);
  for (const key of ["workspace_dir", "sessions", "entries", "fts_messages"]) {
    if (!(key in status)) throw new Error(`status missing ${key}`);
  }
  console.log(`tools/call status OK: sessions=${status.sessions} entries=${status.entries}`);

  if ("structuredContent" in statusRes && statusRes.structuredContent !== undefined) {
    const sc = statusRes.structuredContent;
    if (JSON.stringify(sc) !== JSON.stringify(status)) {
      throw new Error(`structuredContent != parsed text content`);
    }
    console.log("tools/call structuredContent OK: matches text content");
  }

  // tools/call search — list-returning tool. Text payload must be a list.
  const sr = await client.callTool({ name: "search", arguments: { query: "hello", limit: 5 } });
  const hits = JSON.parse((sr.content as { text: string }[])[0].text);
  if (!Array.isArray(hits)) throw new Error(`search not a list: ${typeof hits}`);
  if (hits.length < 1) throw new Error(`search('hello') returned no hits: ${JSON.stringify(hits)}`);
  if (!("ref_text" in hits[0])) throw new Error(`hit missing ref_text: ${JSON.stringify(hits[0])}`);
  console.log(`tools/call search OK: returned ${hits.length} hits`);

  // Empty result must marshal as [], not null.
  const er = await client.callTool({
    name: "search",
    arguments: { query: "definitelynotinthecorpus", limit: 5 },
  });
  const empty = JSON.parse((er.content as { text: string }[])[0].text);
  if (!Array.isArray(empty) || empty.length !== 0) {
    throw new Error(`empty search result not []: ${JSON.stringify(empty)}`);
  }
  console.log("tools/call search empty-result OK: payload is []");

  // Strict argument validation: unknown arg must surface as isError on the
  // result (the spec's reporting mechanism for tool-level failures).
  {
    const res = (await client.callTool({
      name: "search",
      arguments: { query: "x", bogus: 1 } as Record<string, unknown>,
    })) as { isError?: boolean; content?: { text: string }[] };
    if (!res.isError) throw new Error("unknown-arg search call was accepted (no isError)");
    const err = res.content?.[0]?.text ?? "";
    if (!err.includes("unexpected argument") && !err.includes("bogus")) {
      throw new Error(`unexpected error message: ${err}`);
    }
    console.log("tools/call unknown-arg rejection OK");
  }

  // Unknown tool: SDKs may raise OR return isError.
  {
    let detected = false;
    try {
      const res = (await client.callTool({ name: "refresh", arguments: {} })) as { isError?: boolean };
      detected = !!res.isError;
    } catch {
      detected = true;
    }
    if (!detected) throw new Error("unknown tool call was accepted");
    console.log("tools/call unknown-tool rejection OK");
  }

  console.log("aha MCP server is conformant under the official TypeScript SDK client.");
}

main()
  .then(() => transport.close())
  .then(() => process.exit(0))
  .catch((err) => {
    console.error(err);
    transport.close().catch(() => {});
    process.exit(1);
  });
