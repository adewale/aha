"use strict";

const $ = (id) => document.getElementById(id);
const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c]));

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
      return;
    }
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
    Array.from(ol.querySelectorAll("li")).forEach((li, i) => {
      li.addEventListener("click", () => loadRead(hits[i].ref_text));
    });
    if (hits[0] && hits[0].ref_text) loadRead(hits[0].ref_text);
  } catch (e) {
    ol.innerHTML = `<li class="muted">search error: ${esc(e.message)}</li>`;
  }
}

async function loadRead(refText) {
  const pre = $("reader-body");
  pre.textContent = "loading…";
  try {
    const entries = await call("/api/read", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ ref: refText, before: 3, after: 10 }),
    });
    pre.textContent = entries.map((e) =>
      `[${e.timestamp || ""}] ${e.role || ""}:\n${e.text || ""}\n`
    ).join("\n");
  } catch (e) {
    pre.textContent = `read error: ${e.message}`;
  }
}

document.addEventListener("DOMContentLoaded", () => {
  $("search-form").addEventListener("submit", doSearch);
  refreshStatus();
  refreshConflicts();
});
