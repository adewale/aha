"use strict";

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

// Cap rendered text per read entry so a multi-megabyte transcript message
// cannot jank the page. The full content remains available via the API and
// the CLI; this limit is display-only.
const MAX_ENTRY_CHARS = 4000;

function clampText(s) {
  s = String(s || "");
  if (s.length <= MAX_ENTRY_CHARS) return s;
  return s.slice(0, MAX_ENTRY_CHARS) + `\n… [truncated ${s.length - MAX_ENTRY_CHARS} chars; use \`aha read\` for full context]`;
}

let lastHits = [];
let lastIncidents = [];
let incidentState = "all"; // all | unresolved | partial | resolved

async function call(path, init) {
  const r = await fetch(path, init);
  const text = await r.text();
  let body;
  try { body = JSON.parse(text); } catch { body = text; }
  if (!r.ok) {
    const msg = (body && body.error && body.error.message) || String(body);
    throw new Error(msg);
  }
  return body;
}

async function refreshStatus() {
  try {
    const s = await call("/api/status");
    $("status-strip").textContent =
      `${s.sessions || 0} sessions · ${s.entries || 0} entries · ${s.messages || 0} messages · ${s.artifacts || 0} artifacts · ${s.bundles || 0} bundles`;
  } catch (e) {
    $("status-strip").textContent = `status error: ${e.message}`;
  }
}

async function refreshConflicts() {
  try {
    const rows = await call("/api/conflicts");
    const el = $("conflicts");
    if (!rows || !rows.length) {
      el.innerHTML = `<li class="muted">no quarantined conflicts</li>`;
      return;
    }
    el.innerHTML = rows.map((c) => `<li>#${c.id} ${esc(c.session_key)} ${esc(c.entry_id)} <span class="muted">${esc(c.created_at)}</span></li>`).join("");
  } catch (e) {
    $("conflicts").innerHTML = `<li class="muted">conflicts error: ${esc(e.message)}</li>`;
  }
}

// refreshOverview renders the corpus orientation panel: counts, source/machine/
// project composition, time span, and index size: "what am I looking at?".
async function refreshOverview() {
  try {
    const o = await call("/api/overview");
    const chips = (label, arr) =>
      `<div class="ov-row"><span class="ov-label">${label}</span>` +
      (arr && arr.length
        ? arr.map((n) => `<button type="button" class="ov-chip" data-facet="${label}" data-value="${esc(n.name)}">${esc(n.name)} <span class="muted">${n.count}</span></button>`).join("")
        : `<span class="muted">none</span>`) +
      `</div>`;
    const span = o.first_session && o.last_session
      ? `${esc(o.first_session.slice(0, 10))} → ${esc(o.last_session.slice(0, 10))}`
      : "n/a";
    $("overview").innerHTML =
      `<div class="ov-counts">` +
        `<span class="kbd">${o.sessions || 0} sessions</span>` +
        `<span class="kbd">${o.entries || 0} entries</span>` +
        `<span class="kbd">${o.messages || 0} messages</span>` +
        `<span class="kbd">${o.tool_calls || 0} tool calls</span>` +
        `<span class="muted">${span} · ${fmtBytes(o.index_size_bytes || 0)} index</span>` +
      `</div>` +
      chips("source", o.sources) +
      chips("machine", o.machines) +
      chips("project", o.projects);
  } catch (e) {
    $("overview").innerHTML = `<span class="muted">overview error: ${esc(e.message)}</span>`;
  }
}

function fmtBytes(n) {
  if (n < 1024) return `${n} B`;
  const u = ["KB", "MB", "GB"];
  let v = n / 1024, i = 0;
  while (v >= 1024 && i < u.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(1)} ${u[i]}`;
}

// sparkline renders a 12-bucket occurrence histogram as inline SVG (no remote
// charting lib; the dashboard CSP forbids it). Taller bar = more occurrences;
// the rightmost bars are most recent.
function sparkline(spark) {
  const b = spark || [];
  const max = Math.max(1, ...b);
  const w = 4, gap = 1, h = 14;
  const bars = b.map((v, i) => {
    const bh = Math.max(1, Math.round((v / max) * h));
    return `<rect x="${i * (w + gap)}" y="${h - bh}" width="${w}" height="${bh}"></rect>`;
  }).join("");
  const totalW = b.length * (w + gap);
  return `<svg class="spark" width="${totalW}" height="${h}" viewBox="0 0 ${totalW} ${h}" aria-hidden="true">${bars}</svg>`;
}

// ageOf turns an RFC3339 timestamp into a compact "2d ago" string.
function ageOf(ts) {
  if (!ts) return "n/a";
  const then = Date.parse(ts);
  if (isNaN(then)) return "n/a";
  const sec = Math.max(0, (Date.now() - then) / 1000);
  if (sec < 3600) return `${Math.round(sec / 60)}m ago`;
  if (sec < 86400) return `${Math.round(sec / 3600)}h ago`;
  if (sec < 86400 * 30) return `${Math.round(sec / 86400)}d ago`;
  return `${Math.round(sec / (86400 * 30))}mo ago`;
}

function incidentFacets() {
  const f = {};
  const p = $("f-project").value.trim(); if (p) f.project = p;
  const s = $("f-source").value.trim(); if (s) f.source = s;
  const m = $("f-machine").value.trim(); if (m) f.machine = m;
  const t = $("f-tool").value.trim(); if (t) f.tool = t;
  return f;
}

// refreshIncidents loads the unified failure→fix view and renders one row per
// recurring failure carrying both its recurrence and its resolution status,
// filtered by the active state segment and facet inputs.
async function refreshIncidents() {
  const ol = $("incidents");
  try {
    const args = { limit: 50, ...incidentFacets() };
    if (incidentState !== "all") args.state = incidentState;
    setIncidentFeedback(`Loading ${incidentStateLabel(incidentState).toLowerCase()} failures…`);
    const rows = await call("/api/incidents", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    lastIncidents = rows || [];
    if (!lastIncidents.length) {
      ol.innerHTML = `<li class="muted">no failures for this filter. Ingest sessions with tool failures, or widen the filter.</li>`;
      setIncidentFeedback(`No ${incidentStateLabel(incidentState).toLowerCase()} failures for the current filters.`);
      return;
    }
    ol.innerHTML = lastIncidents.map((c, idx) => renderIncident(c, idx)).join("");
    setIncidentFeedback(`Showing ${lastIncidents.length} ${incidentStateLabel(incidentState).toLowerCase()} failure${lastIncidents.length === 1 ? "" : "s"}.`);
  } catch (e) {
    ol.innerHTML = `<li class="muted">incidents error: ${esc(e.message)}</li>`;
    setIncidentFeedback(`Failure list error: ${e.message}`);
  }
}

function incidentStateLabel(state) {
  switch (state) {
  case "unresolved": return "Needs attention";
  case "partial": return "Sometimes fixed";
  case "resolved": return "Fixed before";
  default: return "All recurring";
  }
}

function setIncidentFeedback(text) {
  $("incident-feedback").textContent = text;
}

function renderIncident(c, idx) {
  const stateBadge =
    c.state === "resolved" ? `<span class="kbd st-resolved">✓ resolved</span>`
    : c.state === "partial" ? `<span class="kbd st-partial">◑ partial</span>`
    : `<span class="kbd st-unresolved">● unresolved</span>`;
  const fixes = (c.state === "unresolved" || !(c.paths || []).length)
    ? `<div class="incident-fixes muted">no confirmed fix yet</div>`
    : `<div class="incident-fixes">` +
        `<div class="skill-paths-label muted">fixes that worked` +
          (c.tier ? ` · <span class="kbd tier-${esc(c.tier)}">${esc(c.tier)}</span>` : "") +
          ` <button type="button" class="copy-skill" data-iidx="${idx}">copy fix notes</button></div>` +
        `<ol class="skill-paths">` +
        c.paths.map((p) => {
          const fams = (p.families || []).map(esc).join(" › ");
          const confClass = p.confidence >= 0.6 ? " conf-high" : "";
          const ref = p.sample_ref || "";
          const ord = Number.isInteger(p.sample_ordinal) ? p.sample_ordinal : 0;
          return `<li class="skill-path${ref ? "" : " no-ref"}"${ref ? ` data-ref="${esc(ref)}" data-ordinal="${ord}"` : ""}>` +
            `<div class="meta"><span class="kbd${confClass}">conf=${(p.confidence || 0).toFixed(2)}</span>` +
            `<span class="muted">×${p.support} · ${p.distinct_sessions} sessions · ${p.distinct_projects} projects</span>` +
            (ref ? ` <button type="button" class="trace-btn" data-ref="${esc(ref)}" data-ordinal="${ord}">trace</button>` : "") +
            `</div><div class="snippet"><code>${fams}</code></div>` +
            `<div class="trajectory" hidden></div></li>`;
        }).join("") +
        `</ol></div>`;
  const headRef = c.sample_ref ? ` data-ref="${esc(c.sample_ref)}"` : "";
  return `<li class="incident st-${c.state}"${headRef}>` +
    `<div class="meta">${stateBadge}` +
      `<span class="kbd">×${c.episodes}</span>` +
      `<button type="button" class="incident-scope muted" data-iidx="${idx}">search matching history (${c.distinct_sessions} sessions · ${c.distinct_projects} projects)</button> · ` +
      `${esc(c.tool_name || "?")} · <code>${esc(c.command_family || "")}</code>` +
      `<span class="incident-recency muted">${sparkline(c.spark)} ${ageOf(c.last_seen)}</span>` +
    `</div>` +
    `<div class="snippet">${esc(c.error_signature || "").slice(0, 600)}</div>` +
    fixes +
  `</li>`;
}

// skillDraft renders an incident's top resolution path as copy-pastable fix
// notes. The dashboard never writes or installs skills itself.
function skillDraft(c) {
  const top = (c.paths || [])[0];
  const steps = top ? (top.families || []) : [];
  const name = (c.command_family || c.tool_name || "fix").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
  const lines = [
    `Fix notes: ${name}`,
    "",
    `Trigger: \`${c.tool_name}\` failing with "${c.error_signature}".`,
    "",
    `Observed in ${c.resolved}/${c.episodes} episodes (${(c.resolution_rate * 100).toFixed(0)}% resolved, ${c.tier || "tentative"}).`,
    "",
    "Resolution path that worked",
    "",
    ...steps.map((f, i) => `${i + 1}. ${f}`),
    "",
    top && top.sample_ref ? `Evidence: ${top.sample_ref}` : "",
  ];
  return lines.join("\n");
}

async function copyText(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch (_) { /* fall through */ }
  const ta = document.createElement("textarea");
  ta.value = text;
  ta.style.position = "fixed";
  ta.style.opacity = "0";
  document.body.appendChild(ta);
  ta.select();
  let ok = false;
  try { ok = document.execCommand("copy"); } catch (_) { ok = false; }
  document.body.removeChild(ta);
  return ok;
}

// loadTrajectory expands a fix path into the full fail→fix arc (each step
// clickable to read), via the incident_trajectory tool.
async function loadTrajectory(ref, ordinal, container) {
  if (!container.hidden) { container.hidden = true; container.innerHTML = ""; return; }
  container.hidden = false;
  container.innerHTML = `<span class="muted">loading trajectory…</span>`;
  try {
    const steps = await call("/api/incident_trajectory", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ref, ordinal }),
    });
    if (!steps || !steps.length) {
      container.innerHTML = `<span class="muted">no trajectory available</span>`;
      return;
    }
    container.innerHTML = steps.map((s) =>
      `<div class="traj-step${s.is_error ? " traj-fail" : " traj-ok"}"${s.ref ? ` data-ref="${esc(s.ref)}"` : ""}>` +
        `<span class="traj-dot">${s.is_error ? "✗" : "✓"}</span> <code>${esc(s.family || "")}</code>` +
      `</div>`).join("");
  } catch (e) {
    container.innerHTML = `<span class="muted">trajectory error: ${esc(e.message)}</span>`;
  }
}

async function doSearch(ev) {
  if (ev) ev.preventDefault();
  const args = searchArgs();
  const ol = $("results");
  updateScopeSummary();
  if (!args.query) {
    lastHits = [];
    ol.innerHTML = `<li class="empty-state">Type a prompt, file, command, error, or decision to search across local agent traces.</li>`;
    setSearchFeedback(`${roleFilterLabel()} selected. Type a query to search.`);
    return;
  }
  const query = args.query;
  for (const k of Object.keys(args)) if (args[k] === undefined) delete args[k];
  setSearchFeedback(`Searching ${roleFilterLabel().toLowerCase()} for “${query}”…`);
  ol.innerHTML = `<li class="empty-state">Searching traces…</li>`;
  try {
    const hits = await call("/api/search", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    lastHits = hits || [];
    if (!lastHits.length) {
      ol.innerHTML = `<li class="empty-state">No trace cards found. Try all history, clear scope, or search fewer words.</li>`;
      setSearchFeedback(`No matches for “${query}”.`);
      return;
    }
    const traceCount = new Set(lastHits.map((h, i) => h.session_key || `hit-${i}`)).size;
    ol.innerHTML = renderTraceCards(lastHits);
    setSearchFeedback(`${lastHits.length} matched event${lastHits.length === 1 ? "" : "s"} in ${traceCount} trace card${traceCount === 1 ? "" : "s"}. First trace selected.`);
    if (lastHits[0] && lastHits[0].ref_text) loadRead(lastHits[0].ref_text);
  } catch (e) {
    ol.innerHTML = `<li class="empty-state">Search error: ${esc(e.message)}</li>`;
    setSearchFeedback(`Search error: ${e.message}`);
  }
}

function searchArgs() {
  return {
    query: $("query").value.trim(),
    role: $("role").value.trim() || undefined,
    project: $("project").value.trim() || undefined,
    source: $("source").value.trim() || undefined,
    machine: $("machine").value.trim() || undefined,
    path: $("path").value.trim() || undefined,
    limit: 50,
  };
}

function setSearchFeedback(text) {
  $("search-feedback").textContent = text;
}

function roleFilterLabel() {
  const role = $("role").value.trim();
  if (role === "user") return "Prompts";
  if (role === "assistant") return "Assistant replies";
  if (role === "toolResult") return "Tool output";
  return "All history";
}

function renderTraceCards(hits) {
  const groups = [];
  const bySession = new Map();
  hits.forEach((h, i) => {
    const key = h.session_key || `hit-${i}`;
    if (!bySession.has(key)) {
      const g = { first: h, hits: [] };
      bySession.set(key, g);
      groups.push(g);
    }
    bySession.get(key).hits.push({ h, i });
  });
  return groups.map((g) => {
    const firstIdx = g.hits[0].i;
    return `<li><button type="button" class="trace-card" data-idx="${firstIdx}">` +
      `<div class="trace-head">` +
        `<div><div class="trace-title">${esc(traceTitle(g.hits))}</div>` +
        `<div class="trace-meta">${esc(sessionTitle(g.first))} · ${g.hits.length} matched event${g.hits.length === 1 ? "" : "s"}</div></div>` +
        `<span class="trace-status ${esc(traceStatusClass(g.hits))}">${esc(traceStatus(g.hits))}</span>` +
      `</div>` +
      renderTraceTimeline(g.hits) +
      `<div class="trace-events">` + g.hits.slice(0, 4).map(({ h }) => renderTraceEvent(h)).join("") + `</div>` +
      (g.hits.length > 4 ? `<div class="trace-more muted">+${g.hits.length - 4} more matched events in this trace</div>` : "") +
    `</button></li>`;
  }).join("");
}

function traceTitle(hits) {
  const userHit = hits.find(({ h }) => h.role === "user");
  const chosen = (userHit || hits[0]).h;
  const text = cleanSnippet(chosen.snippet || "");
  return text ? text.slice(0, 120) : sessionTitle(chosen);
}

function cleanSnippet(s) {
  return String(s || "").replace(/[\[\]]/g, "").replace(/\s+/g, " ").trim();
}

function renderTraceTimeline(hits) {
  const dots = hits.slice(0, 10).map(({ h }) =>
    `<span class="trace-dot dot-${esc(roleClass(h.role))}" title="${esc(roleLabel(h.role))}"></span>`).join("");
  return `<div class="trace-timeline" aria-label="matched events timeline">${dots}</div>`;
}

function renderTraceEvent(h) {
  return `<div class="trace-event role-${esc(roleClass(h.role))}">` +
    `<span class="event-role">${esc(roleLabel(h.role))}</span>` +
    `<span class="event-snippet">${esc(cleanSnippet(h.snippet)).slice(0, 320)}</span>` +
  `</div>`;
}

function roleLabel(role) {
  switch (role) {
  case "user": return "Prompt";
  case "assistant": return "Assistant";
  case "toolResult": return "Tool output";
  case "artifact": return "File artifact";
  default: return role || "Event";
  }
}

function roleClass(role) {
  switch (role) {
  case "user": return "prompt";
  case "assistant": return "assistant";
  case "toolResult": return "tool";
  case "artifact": return "file";
  default: return "event";
  }
}

function traceStatus(hits) {
  const text = hits.map(({ h }) => `${h.role || ""} ${h.snippet || ""}`).join("\n").toLowerCase();
  if (/\b(error|failed|failure|panic|exception|denied)\b/.test(text)) return "failure match";
  if (hits.some(({ h }) => h.role === "toolResult")) return "tool trace";
  if (hits.some(({ h }) => h.role === "artifact")) return "file match";
  return "conversation";
}

function traceStatusClass(hits) {
  const s = traceStatus(hits);
  if (s.includes("failure")) return "status-failure";
  if (s.includes("tool")) return "status-tool";
  if (s.includes("file")) return "status-file";
  return "status-conversation";
}

function sessionTitle(h) {
  const project = h.project || "no project";
  const machine = h.machine || "unknown machine";
  const source = h.source || "unknown source";
  const day = h.timestamp ? h.timestamp.slice(0, 10) : "unknown date";
  return `${project} · ${source} · ${machine} · ${day}`;
}

// loadRead fetches surrounding context for a ref and renders it. Selecting a
// result also writes the ref into the URL fragment so the view is reloadable
// and shareable. Refs are stable identifiers by design.
async function loadRead(refText, updateHash = true, window = { before: 3, after: 10 }) {
  if (!refText) return;
  const pre = $("reader-body");
  pre.textContent = "loading…";
  if (updateHash) {
    const next = "#ref=" + encodeURIComponent(refText);
    if (location.hash !== next) history.replaceState(null, "", next);
  }
  try {
    const entries = await call("/api/read", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ref: refText, before: window.before, after: window.after }),
    });
    pre.textContent = entries.map((e) => {
      const body = e.text || e.raw_json || "";
      const label = e.text ? "" : " (raw_json)";
      return `[${e.timestamp || ""}] ${e.role || ""}${label}:\n${clampText(body)}\n`;
    }).join("\n");
  } catch (e) {
    pre.textContent = `read error: ${e.message}`;
  }
}

function refFromHash() {
  const m = location.hash.match(/^#ref=(.+)$/);
  return m ? decodeURIComponent(m[1]) : null;
}

function setIncidentState(next) {
  document.querySelectorAll(".state-btn").forEach((x) => {
    const active = x.dataset.state === next;
    x.classList.toggle("active", active);
    x.setAttribute("aria-pressed", active ? "true" : "false");
  });
  incidentState = next;
  refreshIncidents();
}

function setRoleFilter(role) {
  $("role").value = role;
  document.querySelectorAll(".role-chip").forEach((b) => {
    const active = b.dataset.role === role;
    b.classList.toggle("active", active);
    b.setAttribute("aria-pressed", active ? "true" : "false");
  });
}

function runSearchIfQuery() {
  if ($("query").value.trim()) {
    doSearch();
    return true;
  }
  return false;
}

function updateScopeSummary() {
  const parts = [];
  for (const [id, label] of [["project", "project"], ["source", "source"], ["machine", "machine"], ["path", "path"]]) {
    const value = $(id).value.trim();
    if (value) parts.push(`${label}: ${value}`);
  }
  const box = $("scope-summary");
  if (!parts.length) {
    box.hidden = true;
    $("scope-text").textContent = "";
    return;
  }
  $("scope-text").textContent = `Scoped to ${parts.join(", ")}`;
  box.hidden = false;
}

function clearScope() {
  for (const id of ["project", "source", "machine", "path", "f-project", "f-source", "f-machine", "f-tool"]) $(id).value = "";
  updateScopeSummary();
  refreshIncidents();
  if (!runSearchIfQuery()) setSearchFeedback("Scope cleared. Type a query to search all history.");
  $("query").focus();
}

function focusSearch(hint) {
  $("search-hint").textContent = hint;
  $("search").scrollIntoView({ block: "start", behavior: "smooth" });
  $("query").focus();
}

document.addEventListener("DOMContentLoaded", () => {
  $("search-form").addEventListener("submit", doSearch);
  $("clear-scope").addEventListener("click", clearScope);
  for (const id of ["project", "source", "machine", "path"]) {
    $(id).addEventListener("input", () => {
      updateScopeSummary();
      if (!$("query").value.trim()) setSearchFeedback("Advanced filters updated. Type a query to search within this scope.");
    });
  }
  document.querySelectorAll(".role-chip").forEach((b) =>
    b.addEventListener("click", () => {
      setRoleFilter(b.dataset.role || "");
      if (!runSearchIfQuery()) setSearchFeedback(`${roleFilterLabel()} selected. Type a query to search.`);
      $("query").focus();
    }));
  // Event delegation: one listener for the whole results list.
  $("results").addEventListener("click", (ev) => {
    const card = ev.target.closest("[data-idx]");
    if (!card) return;
    const hit = lastHits[Number(card.dataset.idx)];
    if (hit) {
      setSearchFeedback(`Opened ${roleLabel(hit.role).toLowerCase()} event from ${sessionTitle(hit)}.`);
      loadRead(hit.ref_text);
    }
  });
  // Incident list: one delegated listener handles read drill-in, trajectory
  // expand, skill-draft copy, and cross-link-to-search.
  $("incidents").addEventListener("click", async (ev) => {
    const copyBtn = ev.target.closest("button.copy-skill");
    if (copyBtn) {
      const c = lastIncidents[Number(copyBtn.dataset.iidx)];
      if (c) {
        const ok = await copyText(skillDraft(c));
        copyBtn.textContent = ok ? "copied ✓" : "copy failed";
        setTimeout(() => { copyBtn.textContent = "copy fix notes"; }, 1500);
      }
      return;
    }
    const scopeBtn = ev.target.closest("button.incident-scope");
    if (scopeBtn) {
      const c = lastIncidents[Number(scopeBtn.dataset.iidx)];
      if (c) scopedSearch(c);
      return;
    }
    const traceBtn = ev.target.closest("button.trace-btn");
    if (traceBtn) {
      ev.stopPropagation();
      const container = traceBtn.closest("li.skill-path").querySelector(".trajectory");
      loadTrajectory(traceBtn.dataset.ref, Number(traceBtn.dataset.ordinal), container);
      return;
    }
    const trajStep = ev.target.closest(".traj-step[data-ref]");
    if (trajStep) { loadRead(trajStep.dataset.ref, true, { before: 0, after: 0 }); return; }
    const path = ev.target.closest("li.skill-path[data-ref]");
    if (path) { loadRead(path.dataset.ref, true, { before: 0, after: 0 }); return; }
    const incident = ev.target.closest("li.incident[data-ref]");
    if (incident) loadRead(incident.dataset.ref, true, { before: 0, after: 0 });
  });
  // State segment + facet apply.
  document.querySelectorAll(".state-btn").forEach((b) =>
    b.addEventListener("click", () => setIncidentState(b.dataset.state)));
  $("incident-apply").addEventListener("click", refreshIncidents);
  // Overview chips cross-link into the incident facets.
  $("overview").addEventListener("click", (ev) => {
    const chip = ev.target.closest("button.ov-chip");
    if (!chip) return;
    const facet = chip.dataset.facet, value = chip.dataset.value === "(none)" ? "" : chip.dataset.value;
    const incidentTarget = { source: "f-source", machine: "f-machine", project: "f-project" }[facet];
    const searchTarget = { source: "source", machine: "machine", project: "project" }[facet];
    if (incidentTarget) $(incidentTarget).value = value;
    if (searchTarget) $(searchTarget).value = value;
    updateScopeSummary();
    refreshIncidents();
    focusSearch(value ? `Search within ${facet} ${value}.` : `Search across all ${facet} values.`);
    if (!runSearchIfQuery()) setSearchFeedback(value ? `Scoped to ${facet}: ${value}. Type a query to search.` : `Cleared ${facet} scope. Type a query to search all history.`);
  });
  refreshStatus();
  refreshOverview();
  refreshConflicts();
  refreshIncidents();
  // Restore a deep-linked ref on load.
  const ref = refFromHash();
  if (ref) loadRead(ref, false);
});

// scopedSearch cross-links an incident into the search box: pre-fill the search
// facets from the incident and run a search over its error signature.
function scopedSearch(c) {
  $("query").value = c.error_signature || c.command_family || "";
  setRoleFilter("");
  $("project").value = "";
  $("source").value = "";
  $("machine").value = "";
  $("path").value = "";
  updateScopeSummary();
  setSearchFeedback("Searching history for the selected recurring failure.");
  doSearch();
}
