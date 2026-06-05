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
  return s.slice(0, MAX_ENTRY_CHARS) + `\n… [truncated ${s.length - MAX_ENTRY_CHARS} chars — use \`aha read\` for full context]`;
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
// project composition, time span, and index size — "what am I looking at?".
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
      : "—";
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
// charting lib — the dashboard CSP forbids it). Taller bar = more occurrences;
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
  if (!ts) return "—";
  const then = Date.parse(ts);
  if (isNaN(then)) return "—";
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
    const rows = await call("/api/incidents", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ limit: 50, ...incidentFacets() }),
    });
    lastIncidents = rows || [];
    const shown = lastIncidents.filter((c) => incidentState === "all" || c.state === incidentState);
    if (!shown.length) {
      ol.innerHTML = `<li class="muted">no incidents for this filter — ingest sessions with tool failures, or widen the filter</li>`;
      return;
    }
    ol.innerHTML = shown.map((c) => renderIncident(c, lastIncidents.indexOf(c))).join("");
  } catch (e) {
    ol.innerHTML = `<li class="muted">incidents error: ${esc(e.message)}</li>`;
  }
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
          ` <button type="button" class="copy-skill" data-iidx="${idx}">copy skill draft</button></div>` +
        `<ol class="skill-paths">` +
        c.paths.map((p) => {
          const fams = (p.families || []).map(esc).join(" › ");
          const confClass = p.confidence >= 0.6 ? " conf-high" : "";
          const ref = p.sample_ref || "";
          return `<li class="skill-path${ref ? "" : " no-ref"}"${ref ? ` data-ref="${esc(ref)}"` : ""}>` +
            `<div class="meta"><span class="kbd${confClass}">conf=${(p.confidence || 0).toFixed(2)}</span>` +
            `<span class="muted">×${p.support} · ${p.distinct_sessions} sessions · ${p.distinct_projects} projects</span>` +
            (ref ? ` <button type="button" class="trace-btn" data-ref="${esc(ref)}">trace</button>` : "") +
            `</div><div class="snippet"><code>${fams}</code></div>` +
            `<div class="trajectory" hidden></div></li>`;
        }).join("") +
        `</ol></div>`;
  const headRef = c.sample_ref ? ` data-ref="${esc(c.sample_ref)}"` : "";
  return `<li class="incident st-${c.state}"${headRef}>` +
    `<div class="meta">${stateBadge}` +
      `<span class="kbd">×${c.episodes}</span>` +
      `<button type="button" class="incident-scope muted" data-iidx="${idx}">${c.distinct_sessions} sessions · ${c.distinct_projects} projects</button> · ` +
      `${esc(c.tool_name || "?")} · <code>${esc(c.command_family || "")}</code>` +
      `<span class="incident-recency muted">${sparkline(c.spark)} ${ageOf(c.last_seen)}</span>` +
    `</div>` +
    `<div class="snippet">${esc(c.error_signature || "").slice(0, 600)}</div>` +
    fixes +
  `</li>`;
}

// skillDraft renders an incident's top resolution path as a copy-pastable skill
// stub — the "sessions → skills" payoff, inspect-only (the dashboard never
// writes skills itself).
function skillDraft(c) {
  const top = (c.paths || [])[0];
  const steps = top ? (top.families || []) : [];
  const name = (c.command_family || c.tool_name || "fix").toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "");
  const lines = [
    `# ${name}`,
    "",
    `Trigger: \`${c.tool_name}\` failing with "${c.error_signature}".`,
    "",
    `Observed in ${c.resolved}/${c.episodes} episodes (${(c.resolution_rate * 100).toFixed(0)}% resolved, ${c.tier || "tentative"}).`,
    "",
    "## Resolution that worked",
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
async function loadTrajectory(ref, container) {
  if (!container.hidden) { container.hidden = true; container.innerHTML = ""; return; }
  container.hidden = false;
  container.innerHTML = `<span class="muted">loading trajectory…</span>`;
  try {
    const steps = await call("/api/incident_trajectory", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ref }),
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
  ev.preventDefault();
  const args = {
    query: $("query").value.trim(),
    project: $("project").value.trim() || undefined,
    source: $("source").value.trim() || undefined,
    machine: $("machine").value.trim() || undefined,
    limit: 50,
  };
  if (!args.query) return;
  for (const k of Object.keys(args)) if (args[k] === undefined) delete args[k];
  const ol = $("results");
  ol.innerHTML = `<li class="muted">searching…</li>`;
  try {
    const hits = await call("/api/search", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(args),
    });
    if (!hits.length) {
      ol.innerHTML = `<li class="muted">no hits</li>`;
      lastHits = [];
      return;
    }
    lastHits = hits;
    ol.innerHTML = hits.map((h, i) => `
      <li data-idx="${i}">
        <div class="meta">
          <span class="kbd">${esc(h.source || "?")}</span>
          ${esc(h.role || "")} ·
          ${esc(h.project || "")} ·
          ${esc(h.machine || "")} ·
          ${esc(h.timestamp || "")}
        </div>
        <div class="snippet">${esc(h.snippet || "").slice(0, 600)}</div>
      </li>`).join("");
    if (hits[0] && hits[0].ref_text) loadRead(hits[0].ref_text);
  } catch (e) {
    ol.innerHTML = `<li class="muted">search error: ${esc(e.message)}</li>`;
  }
}

// loadRead fetches surrounding context for a ref and renders it. Selecting a
// result also writes the ref into the URL fragment so the view is reloadable
// and shareable — refs are stable identifiers by design.
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

document.addEventListener("DOMContentLoaded", () => {
  $("search-form").addEventListener("submit", doSearch);
  // Event delegation: one listener for the whole results list.
  $("results").addEventListener("click", (ev) => {
    const li = ev.target.closest("li[data-idx]");
    if (!li) return;
    const hit = lastHits[Number(li.dataset.idx)];
    if (hit) loadRead(hit.ref_text);
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
        setTimeout(() => { copyBtn.textContent = "copy skill draft"; }, 1500);
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
      loadTrajectory(traceBtn.dataset.ref, container);
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
    b.addEventListener("click", () => {
      document.querySelectorAll(".state-btn").forEach((x) => x.classList.remove("active"));
      b.classList.add("active");
      incidentState = b.dataset.state;
      refreshIncidents();
    }));
  $("incident-apply").addEventListener("click", refreshIncidents);
  // Overview chips cross-link into the incident facets.
  $("overview").addEventListener("click", (ev) => {
    const chip = ev.target.closest("button.ov-chip");
    if (!chip) return;
    const facet = chip.dataset.facet, value = chip.dataset.value;
    const target = { source: "f-source", machine: "f-machine", project: "f-project" }[facet];
    if (target) { $(target).value = value === "(none)" ? "" : value; refreshIncidents(); }
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
  $("project").value = "";
  $("source").value = "";
  $("search-form").dispatchEvent(new Event("submit"));
}
