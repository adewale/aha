// HTTP Transport for the aha dashboard server.
//
// Points at a running `aha dashboard` (default 127.0.0.1:18428) and translates
// tool calls into the matching REST routes. GET for no-argument tools; POST
// application/json for argument-bearing tools (`search`, `show`,
// `analyse_failures`, `analyse_failure_trajectory`).
//
// Example:
//   import { aha } from "../aha-mcp.js";
//   import { connectHTTP } from "./http.js";
//
//   const transport = connectHTTP("http://127.0.0.1:18428");
//   const tools = aha(transport);
//   const status = await tools.status();

import type { Transport } from "../aha-mcp.js";

// Mapping from tool name to (method, path). Keep in sync with the route
// registrations in internal/server/server.go.
const ROUTES: Record<string, { method: "GET" | "POST"; path: string }> = {
  aha_capabilities: { method: "GET", path: "/api/v2/capabilities" },
  analyse_failure_trajectory: { method: "POST", path: "/api/v2/analyse/failure-trajectory" },
  analyse_failures: { method: "POST", path: "/api/v2/analyse/failures" },
  overview: { method: "GET", path: "/api/v2/overview" },
  search: { method: "POST", path: "/api/v2/search" },
  show: { method: "POST", path: "/api/v2/show" },
  status: { method: "GET", path: "/api/v2/status" },
  workspace_conflicts: { method: "POST", path: "/api/v2/workspace/conflicts" },
  workspace_size: { method: "GET", path: "/api/v2/workspace/size" },
  workspace_verify: { method: "GET", path: "/api/v2/workspace/verify" },
};

export interface HTTPConnectOptions {
  fetch?: typeof fetch;
  headers?: Record<string, string>;
}

const HTTP_CONTRACT = "aha.http.v2";
const MCP_CONTRACT = "aha.mcp.v2";
const SUPPORTED_REQUIRED_FEATURES = new Set(["read-only-v1", "strict-input-v1", "structured-errors-v1"]);

function validateCapabilities(value: unknown): void {
  const caps = value as { schema?: unknown; http_schema?: unknown; required_features?: unknown; tools?: unknown } | null;
  if (caps?.schema !== MCP_CONTRACT || caps.http_schema !== HTTP_CONTRACT || !Array.isArray(caps.required_features) || !Array.isArray(caps.tools)) {
    throw new Error("compatibility_required: aha HTTP/MCP contract is missing or unsupported");
  }
  for (const feature of caps.required_features) {
    if (typeof feature !== "string" || !SUPPORTED_REQUIRED_FEATURES.has(feature)) {
      throw new Error(`compatibility_required: unsupported required feature ${JSON.stringify(feature)}`);
    }
  }
  const tools = new Set(caps.tools.filter((tool): tool is string => typeof tool === "string"));
  for (const required of Object.keys(ROUTES)) {
    if (!tools.has(required)) throw new Error(`compatibility_required: server does not provide ${required}`);
  }
}

export function connectHTTP(baseURL: string, opts: HTTPConnectOptions = {}): Transport {
  const f = opts.fetch ?? fetch;
  const base = baseURL.replace(/\/+$/, "");

  async function fetchRoute(name: string, args?: unknown): Promise<unknown> {
    const route = ROUTES[name];
    if (!route) throw new Error(`unknown aha tool: ${name}`);
    const init: RequestInit = {
      method: route.method,
      headers: { ...(opts.headers ?? {}), "Aha-HTTP-Contract": HTTP_CONTRACT },
    };
    if (route.method === "POST") {
      (init.headers as Record<string, string>)["Content-Type"] = "application/json";
      init.body = JSON.stringify(args ?? {});
    }
    const r = await f(base + route.path, init);
    const text = await r.text();
    let body: unknown;
    try { body = text ? JSON.parse(text) : null; } catch { body = text; }
    if (!r.ok) {
      const env = body as { error?: { code?: string; message?: string } } | null;
      const message = env?.error?.message ?? `aha HTTP ${r.status}`;
      const code = env?.error?.code ?? "error";
      throw new Error(`${code}: ${message}`);
    }
    return body;
  }

  let capabilities: Promise<unknown> | undefined;
  const negotiate = () => {
    capabilities ??= fetchRoute("aha_capabilities").then((value) => {
      validateCapabilities(value);
      return value;
    });
    return capabilities;
  };

  return {
    async call(name, args) {
      const negotiated = await negotiate();
      if (name === "aha_capabilities") return negotiated;
      return fetchRoute(name, args);
    },
  };
}
