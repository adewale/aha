# TODO

## Agent-friendly roadmap

- Keep `--json` available for every command and enrich schemas over time with stable refs, paths, counts, timestamps, policy/config context, diagnostics, next suggested commands, and machine-readable errors.
- Add explicit agent documentation/skill: recommended `search -> read -> answer` workflow, privacy caveats, which commands are read-only, examples for `--json`, and guidance to avoid answering from snippets alone.
- Continue improving `HitRef` as first-class CLI input/output. Rationale: search results should be round-trippable, meaning a result emitted by `aha search` can be copied directly into `aha read` without reconstructing session/entry IDs from display text. This makes agent workflows deterministic, scriptable, less ambiguous, and suitable for MCP resources or saved search outputs.
- Add qmd-inspired output modes for agent retrieval, such as `--refs`, `--files`, and `--md`, with stable empty-output behavior.
- Make command metadata the source of truth for help, docs sync tests, examples, JSON schema notes, and future MCP tool schemas.
- Add an agent skill/guide for aha, similar to qmd's skill: search for leads, retrieve full source context, then answer with citations/refs.

## Make the aggregated corpus more accessible

- Explore additional search forms beyond SQLite FTS: structured filters, saved searches, semantic/vector search, hybrid ranking, query expansion, and explain/debug output for rankings.
- Consider an MCP interface exposing `search`, `read`, `status`, and `conflicts` as tools/resources before adding any long-running daemon.
- Improve `status` and `doctor`: quick next-action hints in `status`; deeper config/source/corpus/schema/bundle diagnostics in `doctor`.
- Add multi-read or batch-read for agent workflows that need to retrieve several hits from one search.

## Future adapters

- Research and prototype adapters for local stores from Codex CLI, Aider, Cline/Roo Code, Continue, Gemini CLI, Cursor, Windsurf/Codeium, Zed, Goose, and OpenCode.
- Prefer raw-preserving, read-only adapters first; treat Electron/VS Code state DB adapters as experimental until backed by fixtures from real current versions.
