# aha eval rubric

Use this rubric to confirm `aha` is still on track as an agent-history aggregator. Score each criterion 0-2: 0 = absent/regressed, 1 = partial, 2 = strong. A release candidate should have no 0s in P0/P1 rows and should score at least 80% overall.

## P0/P1 product criteria

| Area | What to evaluate | Score guide |
|---|---|---|
| First-run journey | A new user can run `init --accept-secrets`, `refresh`, `search`, then `read` without understanding internals. | 0 broken; 1 works with hidden caveats; 2 documented and tested. |
| Privacy honesty | Docs and CLI accurately state that bundles stay raw, default `none-v1` does not redact, and `redaction:"v1"` redacts corpus projections. | 0 misleading; 1 caveats buried; 2 explicit and verified. |
| Immutable provenance | Snapshot/ingest identity and raw data come from bundled bytes, not mutable live paths. | 0 mutable; 1 mixed; 2 regression-tested. |
| Deterministic snapshots | Same inputs/pinned metadata produce identical bundles. | 0 not deterministic; 1 mostly; 2 tested. |
| Safe local IO | Read-only source adapters; no network imports; symlink/output path escapes blocked. | 0 unsafe; 1 partial; 2 tested. |
| Bounded ingest | Malicious/large bundles cannot exhaust memory/disk before validation. | 0 unbounded; 1 partial; 2 compressed, manifest, entry, checksum, and total budgets. |
| Agent retrieval loop | Search results provide enough stable context for agents to retrieve full source before answering. | 0 snippets only; 1 manual reconstruction; 2 round-trippable refs and read path. |
| Adapter extensibility | New local-history agents can be added without changing archive/corpus/search contracts. | 0 hardcoded; 1 awkward; 2 adapter interface + fixtures. |

## P2 quality criteria

| Area | What to evaluate | Score guide |
|---|---|---|
| CLI composability | Commands reuse a small set of ideas: config/repo flags, output modes, refs, JSON, examples. | 0 ad hoc; 1 mixed; 2 metadata-driven and consistent. |
| Command metadata | Registry includes usage, flags, examples, docs, and JSON schema notes. | 0 absent; 1 partial; 2 doc/test-backed. |
| Output formats | Human, JSON, refs/files, and Markdown renderers are shared and deterministic. | 0 duplicated; 1 partial; 2 centralized and golden-tested. |
| Docs-code sync | README/spec/trust docs are checked against implemented commands, flags, and tests. | 0 stale; 1 shallow; 2 registry/config driven. |
| Test quality | Uses real temp files/SQLite, doc-sync tests, fuzz/property tests, sad-path tests, and race tests. | 0 shallow; 1 mixed; 2 strong. |
| Search quality | Search is deterministic, filterable, and can evolve toward structured/semantic/hybrid search without breaking v1. | 0 brittle; 1 FTS only; 2 stable refs and tested syntax. |
| Corpus accessibility | Status/doctor/MCP/future APIs make the corpus usable by humans and agents. | 0 opaque; 1 CLI only; 2 rich diagnostics/interfaces. |

## Guidance from Claude History Explorer and qmd

- From Claude History Explorer: keep trust claims verifiable, preserve read-only behavior, and optimize the browsing/search/read loop.
- From qmd: snippets are leads, not evidence; search results should include round-trippable refs; status and doctor should provide next actions; agent-facing docs/skills should teach `search -> retrieve -> answer`.
