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
async function loadRead(refText, updateHash = true) {
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
      body: JSON.stringify({ ref: refText, before: 3, after: 10 }),
    });
    pre.textContent = entries.map((e) =>
      `[${e.timestamp || ""}] ${e.role || ""}:\n${clampText(e.text)}\n`
    ).join("\n");
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
  refreshStatus();
  refreshConflicts();
  // Restore a deep-linked ref on load.
  const ref = refFromHash();
  if (ref) loadRead(ref, false);
});
