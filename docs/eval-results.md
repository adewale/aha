# Basic eval results

Date: 2026-05-20
Scope: local working tree after audit/refactor/Codex changes.

## Summary

| Area | Score | Notes |
|---|---:|---|
| First-run journey | 2 | README and CLI tests cover init/default snapshot/ingest/refresh journeys. |
| Privacy honesty | 2 | README/spec/trust docs state no redaction; tests verify trust commands. |
| Immutable provenance | 2 | Bundle-derived Pi identity regression exists. |
| Deterministic snapshots | 2 | Deterministic archive tests pass. |
| Safe local IO | 2 | Read-only/no-network/symlink/path tests pass. |
| Bounded ingest | 2 | Compressed, manifest, entry, checksum, and total budgets plus SHA checks. |
| Agent retrieval loop | 2 | `HitRef` exists, `search --refs` emits copyable refs, and `read <session>#<entry>` works. |
| Adapter extensibility | 2 | Codex added through the adapter interface with fixture tests. |
| CLI composability | 2 | Commands are split per file, share corpus flags/renderers, and registry metadata captures flags/examples/schemas. |
| Command metadata | 2 | Registry includes flags/examples/schema/docs and has a metadata test. |
| Output formats | 2 | Shared renderers exist for search/read/status, search supports refs/files/Markdown, and every command has JSON support. |
| Docs-code sync | 1 | Command/docs tests exist but flag/config schema sync can be deeper. |
| Test quality | 2 | Uses real files/SQLite, fuzz, race, doc-sync, sad paths, and static trust checks. |
| Search quality | 1 | Deterministic FTS and filters exist; structured/semantic/hybrid search remains future work. |
| Corpus accessibility | 1 | CLI status/doctor exist; richer doctor, MCP, and multi-read are TODO. |

Overall: 27 / 30 = 90%.

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
