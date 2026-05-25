# TODO

## Agent-friendly roadmap

- Keep `--json` available for every command and continue enriching schemas over time with paths, counts, timestamps, policy/config context, diagnostics, and next suggested commands. Machine-readable top-level JSON errors and copy-pastable search refs are in place.
- Add explicit agent documentation/skill for `aha`:
  - teach the required `search -> read -> answer` workflow;
  - state that snippets are leads, not evidence;
  - list read-only commands (`search`, `read`, `status`, `verify`, `conflicts`, `doctor`) vs mutating commands (`refresh`, `snapshot`, `ingest`, `verify --repair-fts`, `depot init`, `depot verify --repair`);
  - include JSON examples for `search --json`, `search --refs`, `read <ref> --json`, `status --json`, `verify --json`, and JSON error envelopes;
  - explain privacy caveats: v1 does not redact secrets, R2 is opt-in upload, and bundles/corpora are private;
  - show how an agent should cite/quote refs in its own notes;
  - include failure handling: if search returns nothing, broaden query; if read fails, report the stale ref and run `aha status --json`/`aha verify --json`/`aha doctor --json`;
  - include config/depot caveats: use `--repo` for alternate corpora and do not pass `--depot` to search/read.
- Continue improving canonical refs as first-class CLI input/output. Search JSON now includes structured `ref` plus copy-pastable `ref_text`; next steps are multi-read and MCP resource mapping.
- Keep qmd-inspired output modes for agent retrieval (`--refs`, `--files`, and `--md`) stable and documented.
- Keep command metadata as the source of truth for generated command docs, docs sync tests, examples, JSON schema notes, and future MCP tool schemas; eventually remove remaining manual flag-definition duplication.
- Add an agent skill/guide for aha, similar to qmd's skill: search for leads, retrieve full source context, then answer with citations/refs.

## Make the aggregated corpus more accessible

- Explore additional search forms beyond SQLite FTS: structured filters, saved searches, semantic/vector search, hybrid ranking, query expansion, and explain/debug output for rankings.
- Consider an MCP interface exposing `search`, `read`, `status`, and `conflicts` as tools/resources before adding any long-running daemon.
- Continue improving `doctor`: source, corpus, depot, and common R2 misconfiguration diagnostics exist; remaining depth includes schema migration details, bundle blob-store checks, adapter fixture/version drift checks, and optional R2 bucket setting verification where Cloudflare exposes it.
- Add multi-read or batch-read for agent workflows that need to retrieve several hits from one search. A likely syntax is `aha read --refs-file refs.txt --json` or `aha read <ref1> <ref2> ... --json`, returning grouped context per ref.

## Performance work

- Use `docs/performance-scalability-plan.md` as the implementation order for performance work, including Phase-0 abstraction-readiness characterization, measurable scenario metrics, PBT performance invariants for many trivial bundles/duplicate refs, and package-level pprof before CLI-level profiling.
- Continue tracking `aha verify` with pathological benchmarks; the rowid/query-plan fix removed the first superlinear cliff, but future FTS changes must keep the cheap query-plan guard green.
- Add byte/call counters for archive/depot/ingest known-SHA handoffs so future regressions show up as repeated hash/read passes.
- Expand PBT/operation-count coverage for `state_sha256` refresh across duplicate refs, many machines, and catalog orderings.
- Prepare/batch ingest SQL and prefetch per-session duplicate/conflict state to reduce per-entry SQL chatter.
- Add indexed project/path filtering and cap/warn high-limit broad searches.
- Expand quick/deep depot verify/status operation-budget tests, especially for R2 repair and deep verification accounting.

## Future adapters

- Research and prototype adapters for local stores from Aider, Cline/Roo Code, Continue, Gemini CLI, Cursor, Windsurf/Codeium, Zed, and Goose.
- Punt OpenCode support unless/until users provide current fixtures or specifically request it. Research is parked in `docs/research/opencode.md`.
- Prefer raw-preserving, read-only adapters first; treat Electron/VS Code state DB adapters as experimental until backed by fixtures from real current versions.
