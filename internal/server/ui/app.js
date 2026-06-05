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
let lastClusters = [];
let lastSkillRefs = [];

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

// refreshClusters loads ranked tool-call failure clusters. Each row shows the
// recurrence signal (count, distinct sessions/projects) plus the normalized
// command family and error signature — the shape a human scans to decide
// whether the pattern is a skill candidate. Selecting a row drills into a sample failing
// command via the same read view search results use.
async function refreshClusters() {
  const ol = $("clusters");
  try {
    const rows = await call("/api/clusters", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ limit: 50 }),
    });
    if (!rows || !rows.length) {
      ol.innerHTML = `<li class="muted">no error clusters yet — ingest sessions with tool failures to populate this</li>`;
      lastClusters = [];
      return;
    }
    lastClusters = rows;
    ol.innerHTML = rows.map((c, i) => `
      <li data-cidx="${i}"${c.sample_ref ? "" : ' class="no-ref"'}>
        <div class="meta">
          <span class="kbd">×${c.count}</span>
          <span class="muted">${c.distinct_sessions} sessions · ${c.distinct_projects} projects</span> ·
          ${esc(c.tool_name || "?")} ·
          <code>${esc(c.command_family || "")}</code>
        </div>
        <div class="snippet">${esc(c.error_signature || "").slice(0, 600)}</div>
      </li>`).join("");
  } catch (e) {
    ol.innerHTML = `<li class="muted">clusters error: ${esc(e.message)}</li>`;
  }
}

// refreshSkillCandidates loads outcome-weighted skill candidates: clusters
// whose failures have at least one observed fix. Each row shows the cluster
// identity, its resolution rate and tier, and top-K resolution paths ranked
// by Wilson-bound confidence — so a 1/1 fix never appears above a 3/4 one.
// Selecting a path drills into a sample resolving success via the read view.
async function refreshSkillCandidates() {
  const ol = $("skills");
  try {
    const rows = await call("/api/skill_candidates", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ limit: 50 }),
    });
    if (!rows || !rows.length) {
      ol.innerHTML = `<li class="muted">no resolved clusters yet — ingest sessions where a failing command later succeeds</li>`;
      lastSkillRefs = [];
      return;
    }
    const refs = [];
    ol.innerHTML = rows.map((c) => {
      const paths = (c.paths || []).map((p) => {
        const idx = refs.length;
        refs.push(p.sample_ref || "");
        const fams = (p.families || []).map(esc).join(" › ");
        return `
          <li class="skill-path${p.sample_ref ? "" : " no-ref"}" data-skidx="${idx}">
            <div class="meta">
              <span class="kbd">conf=${p.confidence.toFixed(2)}</span>
              <span class="muted">×${p.support} · ${p.distinct_sessions} sessions · ${p.distinct_projects} projects</span>
            </div>
            <div class="snippet"><code>${fams}</code></div>
          </li>`;
      }).join("");
      return `
        <li class="skill">
          <div class="meta">
            <span class="kbd">${esc(c.tier || "")}</span>
            <span class="muted">${c.resolved}/${c.episodes} resolved · rate=${(c.resolution_rate || 0).toFixed(2)}</span> ·
            ${esc(c.tool_name || "?")} ·
            <code>${esc(c.command_family || "")}</code>
          </div>
          <div class="snippet">${esc(c.error_signature || "").slice(0, 600)}</div>
          <ol class="skill-paths">${paths}</ol>
        </li>`;
    }).join("");
    lastSkillRefs = refs;
  } catch (e) {
    ol.innerHTML = `<li class="muted">skill candidates error: ${esc(e.message)}</li>`;
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
  $("clusters").addEventListener("click", (ev) => {
    const li = ev.target.closest("li[data-cidx]");
    if (!li) return;
    const c = lastClusters[Number(li.dataset.cidx)];
    if (c && c.sample_ref) loadRead(c.sample_ref, true, { before: 0, after: 0 });
  });
  $("skills").addEventListener("click", (ev) => {
    const li = ev.target.closest("li[data-skidx]");
    if (!li) return;
    const ref = lastSkillRefs[Number(li.dataset.skidx)];
    if (ref) loadRead(ref, true, { before: 0, after: 0 });
  });
  refreshStatus();
  refreshConflicts();
  refreshClusters();
  refreshSkillCandidates();
  // Restore a deep-linked ref on load.
  const ref = refFromHash();
  if (ref) loadRead(ref, false);
});
