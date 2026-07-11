import { readFileSync, realpathSync } from "node:fs";
import { isAbsolute, relative } from "node:path";

function under(root: string, candidate: string): boolean {
  const rel = relative(root, candidate);
  return rel !== "" && rel !== ".." && !rel.startsWith(`..${process.platform === "win32" ? "\\" : "/"}`) && !isAbsolute(rel);
}

// assertAttestedConformance rejects ambient AHA_BIN/AHA_CONFIG values unless
// scripts/verify.sh created and attested the private root that owns both.
export function assertAttestedConformance(): { ahaBin: string; ahaConfig: string } {
  const rootInput = process.env.AHA_MCP_CONFORMANCE_ROOT;
  const token = process.env.AHA_MCP_CONFORMANCE_TOKEN;
  const binInput = process.env.AHA_BIN;
  const configInput = process.env.AHA_CONFIG;
  if (!rootInput || !token || !binInput || !configInput) {
    throw new Error("missing isolated MCP conformance attestation; run scripts/verify.sh mcp");
  }
  const root = realpathSync(rootInput);
  const marker = readFileSync(`${root}/.aha-mcp-conformance`, "utf8").trim();
  if (marker !== token) throw new Error("invalid MCP conformance attestation");
  const ahaBin = realpathSync(binInput);
  const ahaConfig = realpathSync(configInput);
  if (!under(root, ahaBin) || !under(root, ahaConfig)) {
    throw new Error("MCP conformance binary/config must be owned by the attested workspace");
  }
  return { ahaBin, ahaConfig };
}
