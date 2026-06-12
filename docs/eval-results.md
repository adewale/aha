# Basic eval results

Date: 2026-05-20
Scope: local working tree after audit/refactor/Codex changes.

*Historical: predates depot v2 ([docs/depot-v2-spec.md](depot-v2-spec.md)). Bundle-based depot mechanics referenced below were replaced by content-addressed snapshots in June 2026.*

## Summary

| Area | Score | Notes |
|---|---:|---|
| First-run journey | 2 | README and CLI tests cover init/default snapshot/ingest/refresh journeys. |
| Privacy honesty | 2 | README/spec/trust docs state no redaction; tests verify trust commands. |
| Immutable provenance | 2 | Bundle-derived Pi identity regression exists. |
| Deterministic snapshots | 2 | Deterministic archive tests pass. |
| Safe local IO | 2 | Read-only/no-network/symlink/path tests pass. |
| Bounded ingest | 2 | Compressed, manifest, entry, checksum, and total budgets plus SHA checks. |
| Agent retrieval loop | 2 | `search --refs` emits copyable canonical refs, and `read <ref_text>` works for `msg:v1`, `session:v1`, and `artifact:v1` refs. |
| Adapter extensibility | 2 | Codex added through the adapter interface with fixture tests. |
| CLI composability | 2 | Commands are split per file, share corpus flags/renderers, and registry metadata captures flags/examples/schemas. |
| Command metadata | 2 | Registry includes flags/examples/schema/docs and has a metadata test. |
| Output formats | 2 | Shared renderers exist for search/read/status, search supports refs/files/Markdown, and every command has JSON support. |
| Docs-code sync | 1 | Command/docs tests exist but flag/config schema sync can be deeper. |
| Test quality | 2 | Uses real files/SQLite, fuzz, race, doc-sync, sad paths, and static trust checks. |
| Search quality | 1 | Deterministic FTS and filters exist; structured/semantic/hybrid search remains future work. |
| Corpus accessibility | 2 | CLI status/doctor/verify exist, including `verify --repair-fts`; MCP and multi-read remain TODO. |

Overall: 28 / 30 = 93%.

## Basic commands run

```bash
git diff --check
go test ./...
go vet ./...
go test -race ./...
go test ./internal/adapters -run=^$ -fuzz=FuzzParseGenericJSONL -fuzztime=2s
go build -o /tmp/aha ./cmd/aha
```

## Verdict

On track, with the main remaining agent-friendliness gaps captured in `TODO.md`: richer standalone agent docs/skill, MCP, multi-read, golden output tests, and advanced search modes.
