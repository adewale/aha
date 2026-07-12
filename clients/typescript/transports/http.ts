// HTTP Transport for the aha dashboard server.
//
// Points at a running `aha dashboard` (default 127.0.0.1:18428) and translates
// tool calls into the matching REST routes. GET for no-argument tools; POST
// application/json for argument-bearing tools (`search`, `read`, `incidents`,
// `incident_trajectory`).
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
  search: { method: "POST", path: "/api/search" },
  read: { method: "POST", path: "/api/read" },
  incidents: { method: "POST", path: "/api/incidents" },
  incident_trajectory: { method: "POST", path: "/api/incident_trajectory" },
  overview: { method: "GET", path: "/api/overview" },
  status: { method: "GET", path: "/api/status" },
  verify: { method: "GET", path: "/api/verify" },
  conflicts: { method: "GET", path: "/api/conflicts" },
  corpus_size: { method: "GET", path: "/api/corpus_size" },
  doctor: { method: "GET", path: "/api/doctor" },
};

export interface HTTPConnectOptions {
  fetch?: typeof fetch;
  headers?: Record<string, string>;
}

export function connectHTTP(baseURL: string, opts: HTTPConnectOptions = {}): Transport {
  const f = opts.fetch ?? fetch;
  const base = baseURL.replace(/\/+$/, "");
  return {
    async call(name, args) {
      const route = ROUTES[name];
      if (!route) throw new Error(`unknown aha tool: ${name}`);
      const init: RequestInit = {
        method: route.method,
        headers: { ...(opts.headers ?? {}) },
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
    },
  };
}
