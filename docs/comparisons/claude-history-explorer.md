# Comparison: `aha` and `claude-history-explorer`

This comparison records what `aha` learned from [`adewale/claude-history-explorer`](https://github.com/adewale/claude-history-explorer) (CHE) and what we intentionally do differently.

## Source material checked

- CHE `README.md`, `FAQ.md`, `TRUST.md`
- CHE `docs/ARCHITECTURE.md`, `docs/DOMAIN_MODEL.md`, `docs/JSON_SCHEMAS.md`
- CHE parser/project/model modules
- CHE tests, including read-only/static checks and schema-alignment fixtures

No `LESSONS_LEARNED.md` file was present in the current CHE checkout inspected for this comparison. The nearest equivalent is CHE's trust/architecture/test corpus, especially `TRUST.md`.

## What `aha` adopted

| CHE lesson | `aha` implementation |
|---|---|
| Claude Code history lives under `~/.claude/projects/`. | Built-in `claude-code` adapter default root. |
| Project directories encode paths such as `-Users-foo-project`. | Best-effort decoded path metadata while preserving raw path identity. |
| `agent-*.jsonl` files are subagent sessions. | Discovery marks Claude `agent-*` files as subagents and tests assert ingestion. |
| JSONL parsing must skip malformed lines gracefully. | Line parser records diagnostics and continues. |
| `message.content` may be string or list of blocks. | Parser supports strings, text blocks, tool blocks, and image blocks. |
| `tool_result` should not become normal searchable conversation text. | V1 preserves raw tool output but does not index it by default. |
| Read-only history access should be a trust claim backed by tests. | Snapshot read-only tests plus adapter static tests guard source adapters from write/mutation calls. |
| Schema/documentation alignment tests prevent drift. | README command/privacy sync and cycle-ledger sync tests. |
| Small, auditable trust model improves user confidence. | `docs/trust.md` documents privacy, source access, local-only behavior, and verification commands. |

## Intentional differences

| Area | CHE | `aha` |
|---|---|---|
| Scope | Single-source Claude Code explorer. | Multi-source Pi + Claude Code archive/corpus. |
| Storage | Parse local files on demand. | Snapshot immutable bundles, ingest into SQLite + blobs. |
| Privacy posture | Read-only/no-network local explorer; wrapped aggregate sharing feature. | Local private archive; no redaction in v1; no hosted/share feature. |
| Search | In-process regex over parsed sessions. | SQLite FTS5 over indexed messages/artifacts. |
| Tool input/output | Searches some tool inputs; skips tool results. | V1 records tool metadata but excludes tool output from default index. |
| History safety | Never writes to Claude history. | Never mutates source histories; writes only bundles/corpus/config. |
| Multi-machine | Not primary goal. | Core bundle/corpus merge goal. |

## Improvements applied after this comparison

- Added `docs/trust.md` with CHE-style verifiable trust claims.
- Added static tests that application packages do not import network APIs.
- Added static tests that source adapters do not contain write/delete/rename filesystem calls.
- Kept real-history smoke testing as a release-readiness requirement because it caught a Pi parsing bug synthetic fixtures missed.

## Remaining CHE-inspired follow-ups

- Add more anonymized real Claude fixtures, especially image/attachment cases.
- Consider a future `stats`/summary command inspired by CHE, but keep it out of v1 unless it serves archive/search goals.
- Consider richer JSON schema documentation for CLI JSON output if `aha` grows external integrations.
