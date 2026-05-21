# TODO

## Agent-friendly roadmap

- Keep `--json` available for every command and continue enriching schemas over time with stable refs, paths, counts, timestamps, policy/config context, diagnostics, and next suggested commands. Machine-readable top-level JSON errors are in place.
- Add explicit agent documentation/skill: recommended `search -> read -> answer` workflow, privacy caveats, which commands are read-only, examples for `--json`, and guidance to avoid answering from snippets alone.
- Continue improving `HitRef` as first-class CLI input/output. Rationale: search results should be round-trippable, meaning a result emitted by `aha search` can be copied directly into `aha read` without reconstructing session/entry IDs from display text. This makes agent workflows deterministic, scriptable, less ambiguous, and suitable for MCP resources or saved search outputs.
- Keep qmd-inspired output modes for agent retrieval (`--refs`, `--files`, and `--md`) stable and documented.
- Keep command metadata as the source of truth for generated command docs, docs sync tests, examples, JSON schema notes, and future MCP tool schemas.
- Add an agent skill/guide for aha, similar to qmd's skill: search for leads, retrieve full source context, then answer with citations/refs.

## Make the aggregated corpus more accessible

- Explore additional search forms beyond SQLite FTS: structured filters, saved searches, semantic/vector search, hybrid ranking, query expansion, and explain/debug output for rankings.
- Consider an MCP interface exposing `search`, `read`, `status`, and `conflicts` as tools/resources before adding any long-running daemon.
- Improve `status` and `doctor`: quick next-action hints exist; deeper config/source/corpus/schema/bundle diagnostics in `doctor` remain.
- Add multi-read or batch-read for agent workflows that need to retrieve several hits from one search. A likely syntax is `aha read --refs-file refs.txt --json` or `aha read <ref1> <ref2> ... --json`, returning grouped context per ref.

## Future adapters

- Research and prototype adapters for local stores from Aider, Cline/Roo Code, Continue, Gemini CLI, Cursor, Windsurf/Codeium, Zed, and Goose.
- Punt OpenCode support unless/until users provide current fixtures or specifically request it. Research is parked in `docs/research/opencode.md`.
- Prefer raw-preserving, read-only adapters first; treat Electron/VS Code state DB adapters as experimental until backed by fixtures from real current versions.
