# Changelog

All notable changes to `aha` are documented here. `aha` has not had a tagged release yet; entries under **Unreleased** describe changes currently on the development branch.

## Unreleased

### Fixed

- Codex adapter now parses the current (enveloped) rollout format. Modern Codex CLI wraps every line as `{timestamp, type, payload}` — the conversation lives inside `response_item` payloads (`payload.type:"message"`, `payload.role`, `payload.content[]` of `input_text`/`output_text`), `session_meta` carries the session id/cwd, and `turn_context` carries the model. The generic JSONL parser looked for top-level `role`/`message.content`, so it ingested every line as an entry but recognized **zero messages** and indexed nothing for search (confirmed on a real install: 8,693 entries, 0 messages). The adapter now detects the enveloped format and unwraps `payload` — mapping user/assistant messages, tool calls (`function_call` name/arguments/command), tool output, and reasoning — while delegating older flat rollouts to the generic parser unchanged. Verified by new conformance + committed-fixture coverage and by re-running the smoketest against a modern rollout (messages and FTS rows now populate and search/read succeed).

### Added

- `aha clusters --with-fixes` — outcome-weighted skill candidates: the second half of the "sessions → skills" direction. The plain `clusters` surface ranks recurring tool-call *failures* by recurrence and spread but stops short of the fix. `--with-fixes` mines single-session failure→fix arcs (new append-only `failure_episodes` table, migration 14, projected from the `tool_invocations` *successes* already stored) and ranks each resolved cluster by the resolution path that actually worked. Path confidence is a Wilson lower bound over the cluster's resolved-episode count, so a one-off fix (1/1) never outranks a repeatedly-confirmed one (3/4); confounded clusters surface their top-K competing paths rather than a single confidently-wrong recommendation; each candidate carries a resolution rate, a `tentative`/`established` tier, and a `sample_ref` to the resolving success. Episodes are rebuilt at ingest from the stored (already-redacted) invocations and backfilled for existing corpora — no new ingest, no new redaction boundary. v1 is single-session and hard-signal-only (`is_error`/`exit_code`); a fix is only credited when the *same* command family that failed later succeeds. Surfaced across every read-only entry point: CLI (`aha clusters --with-fixes`), a dedicated `skill_candidates` MCP tool, `POST /api/skill_candidates` on the dashboard server, a new `resolved clusters — outcome-weighted fixes` section on the dashboard UI with click-to-read drill-in to the resolving success, and typed `SkillCandidate`/`ResolutionPath`/`SkillCandidatesArgs` + `aha().skill_candidates()` in the regenerated TypeScript client. MCP exposes it as a separate tool (not a `clusters` flag) so the chooser model picks "what worked" vs "what failed" by name. New `internal/corpus` units: `assembleEpisodes` (`episodes.go`), `wilsonLowerBound`/`spread`/`pathScore` (`scoring.go`), `failure_episodes` migration/backfill, and `SkillCandidates` (`outcomes.go`). See `docs/outcome-weighting-spec.md`.
- Unified fake-source fixtures for every supported coding agent and an all-sources end-to-end test. `testutil.WriteAgentFixtures` now also seeds a fake OpenCode SQLite database (`opencode.db`, matching the `session`/`message`/`part` schema) alongside the existing Pi/Claude/Codex trees, with `FixtureRoots.OpenCodeRoot`. `TestEndToEndAllSources` (`internal/corpus`) drives all four agents through the full pipeline — discovery → snapshot → bundle → ingest → search → read — and asserts each source's distinctive needle is searchable and reads back to real context. Separate regression coverage verifies OpenCode model/token/tool metadata persistence, duplicate session IDs across release-channel DBs, and modern Codex tool-call metadata rows. The fixture databases are seeded deterministically from committed SQL (kept readable/reviewable) rather than committed as opaque binaries.
- `scripts/smoketest.sh <opencode|codex|claude|pi> [SOURCE_ROOT]` — a safe, read-only smoketest that verifies an adapter against a real machine. Portable to macOS's default bash 3.2: uses parallel indexed arrays instead of associative arrays; checks table existence via sqlite3 `.tables` (bare names) rather than grepping quoted DDL, so it matches backtick/bracket/double-quote quoting; and counts the JSONL export with `wc` instead of piping `find` into `grep -q` (which under `pipefail` reported a spurious failure when `grep` closed the pipe early). It reports ingestion depth (sessions/entries/messages/FTS rows from `status`) and hard-fails when an adapter discovers session files but ingests zero messages or indexes zero searchable text, so a real-format extraction gap surfaces as a failure. It builds/uses the checkout binary against a throwaway config and writes every artifact (corpus, depot, config, cache/build cache, and the OpenCode JSONL export) under a single `/tmp` directory. It runs discovery → snapshot → ingest → search → read end-to-end, and checks the source was not modified by comparing before/after content fingerprints (plus, for OpenCode, database/sidecar content hashes and `integrity_check`). `AHA_OPENCODE_EXPORT_DIR` is what lets the script keep the generated JSONL export under `/tmp` cross-platform instead of the user cache directory.
- OpenCode source adapter (`opencode`). OpenCode stores history in a SQLite database (`$XDG_DATA_HOME/opencode/opencode.db` when XDG is set, otherwise `~/.local/share/opencode/opencode.db`; `$OPENCODE_DB` is an exclusive DB-file override; release-channel `opencode-*.db` files are included when no override is set) rather than JSONL, so the adapter converts the database into deterministic, lossless JSONL session files during `Discover` and then parses them like any other JSONL source. The full `data` JSON of every `session`/`message`/`part` row is preserved verbatim (lossless reserialization, not a lossy projection); message text, tool calls, model/token/cost metadata, and image file parts are extracted where the known part shapes are present. The DB and any currently present `-wal`/`-shm` sidecars are copied into a private, locked, per-database export directory under the user cache before reading; the source database is never written to. The conversion lives in a separate `internal/opencodeexport` package so the source adapters keep their textual read-only invariant (no filesystem-mutation calls in `internal/adapters`). Output is byte-stable for identical database contents, so the snapshot unchanged-state fingerprint (which includes each captured file's path and hash) still detects "nothing changed" and avoids re-uploading.

### Changed

- Migrated the MCP server from a hand-rolled JSON-RPC implementation to the official `github.com/modelcontextprotocol/go-sdk` v1.6.1. Wire format, framing, lifecycle, schema derivation, structured-content emission, and tool annotations are now the SDK's responsibility; `internal/mcp` keeps only typed input structs (with `jsonschema` tags), the pure business functions, and the `CallTool` dispatch the HTTP dashboard reuses. Deletes `protocol.go`, `protocol_test.go`, `protocol_fuzz_test.go` (~350 LOC of test/source the SDK now owns); rewrites `tools_test.go` against `NewInMemoryTransports`. Six-leg conformance suite (Python / TS / Go SDKs × server / client) still green, plus the Code Mode workflow conformance (`scripts/mcp-conformance/codemode_workflow.ts`) that drives the canonical search → filter → parallel-read fan-out via the typed `clients/typescript/aha-mcp.ts` surface, plus an in-process `TestHTTPAndMCPPathsAreConsistent` that pins HTTP-dispatch and MCP-dispatch to the same payload.
- After the migration: removed `rejectExtras[T]` and `objectResult` helpers from `internal/mcp/tools.go`. Both were workarounds for SDK behaviour the SDK actually does itself: `applySchema` validates against the auto-derived input schema (with `additionalProperties: false` for structs), and `CallToolResult.StructuredContent` + `content[].text` are auto-filled from a non-nil typed `Out` return. Object tools now return `(nil, out, nil)` and let the SDK fill both fields; list tools keep `Out=any` (the SDK refuses array output schemas) and use a renamed `textResult` helper. `tools.go` is ~47 LOC shorter for the same behaviour.
- Generated TypeScript surface (`clients/typescript/aha-mcp.ts`) carries JSDoc on every field and method now. Field descriptions come from the `jsonschema` struct tag on the corresponding Go input struct; method descriptions come from a canonical `mcp.ToolDescriptions` map that `registerTools` also reads, so wire descriptions and TS docs move together. Extras (FTS5 query syntax, the cwd-vs-project-name reality on `SearchResult.project`) are documented inline.
- `ReadArgs` in the generated TS surface is now a discriminated union (`{ ref, ... } | { session, entry?, ... }`) instead of an interface with five optional fields, so the typed surface pushes callers toward one mode at a time.
- `parseRef` and `formatRef` are now exported from `aha-mcp.ts`. The canonical wire formats (`msg:v1:<sk>:<entry>`, `session:v1:<sk>`, `artifact:v1:<sha>`) were previously documented only in `internal/model/ref.go`; TS consumers can now build and parse them via the typed surface. Round-trip tested in `clients/typescript/test/stdio.test.ts`.
- `clients/typescript/transports/stdio.ts` now throws a typed `AhaMcpError` (with stable `.code` values: `"tool_error"`, `"transport_closed"`, or a stringified JSON-RPC code) instead of plain `Error`. Callers can branch on the failure mode without regex-matching message text. The HTTP transport's symmetric upgrade is tracked but not in this release.
- `internal/mcp/codegen` no longer panics on unhandled Go kinds. Anonymous structs, novel interfaces, or types added without updating the generator now emit `unknown /* FIXME: ... */` plus a stderr warning. The drift test still catches regenerated-output mismatches; the codegen just doesn't crash during exploration.
- `cmd/aha-gen-ts` emits a provenance header (`@generated by cmd/aha-gen-ts from <module>`) so the source of a checked-in TS file is legible without consulting git history.

### Fixed

- The stdio MCP wire framing was originally LSP-style `Content-Length:` (copied from tracebase). The spec mandates NDJSON (`"Messages are delimited by newlines, and MUST NOT contain embedded newlines."`). Real clients refused to decode our frames. Discovered by the six-leg cross-SDK conformance suite; fixed before the SDK migration retired the framer entirely.
- `search`, `read`, `conflicts` returned Go `nil` for empty results, which marshals to JSON `null`. Discovered by the Python SDK conformance check; tools now return empty slices (`[]search.Result{}` etc.), so the wire shape is always a list.
- `structuredContent` for list-typed tools used to carry the array directly. The official Python SDK models the field as `Dict[str, Any]` and raises a Pydantic validation error on arrays. List-typed tools now omit `structuredContent` entirely and the typed payload travels in `content[].text` for those tools.
- HTTP dashboard `Host` header validation accepted `Host: 127.0.0.1:abc` because `net.SplitHostPort` accepts any string after the last colon as a port. Tightened to require an all-digit port; surfaced by `TestHostHeaderAllowlistRejectsMalformedBrackets` after the audit added defensive coverage.

### Docs

- README repositioning: walked back the "spot patterns and turn them into skills" promise to "examine your behaviour today; pattern detection is the next layer, tracked in `docs/research/agent-trace-tools.md`". The substrate is what ships; the product on top is honestly named as not built.
- `clients/typescript/README.md`: added concrete integration examples for Cloudflare codemode (`createCodeTool` + `ToolProvider`) and Anthropic code-execution-with-MCP (`callMCPTool` filesystem layout), plus an ASCII architecture diagram showing the host/sandbox vs aha boundary and a "What aha provides and what the host provides" matrix. The implicit "host brings the sandbox" assumption is now explicit.
- `docs/mcp-spec.md` moved its "Open questions" section to "Decided" with rationale: `doctor` will not accept `--depot` (CLI covers it); `read` will not gain a `format: "md"` arg (the typed `[]ReadEntry` surface is what callers actually need).

### Added

- `aha mcp`: a read-only stdio MCP (Model Context Protocol) server exposing `search`, `read`, `clusters`, `status`, `verify`, `conflicts`, `corpus_size`, and `doctor` as JSON-RPC tools over the same `internal/corpus`/`internal/search` functions the CLI uses. Strict argument validation; write tools are intentionally not exposed. See `docs/mcp-spec.md`.
- `aha clusters`: ranks recurring tool-call failures by tool, command family, and privacy-preserving error signature as skill-candidate signals. Cluster rows use redacted/normalized display text only and include a sample command ref for drill-in.
- `aha mcp --dry-run`: opens the corpus, registers every tool, prints a one-line summary to stderr, then exits 0 without serving stdio. Pre-flight check for hosts wiring `aha mcp` into their MCP config — proves the binary can find its corpus and confirms the advertised tool set.
- `aha serve`: a read-only local dashboard serving the same tool surface as HTTP/JSON plus a minimal embedded UI (`go:embed`, no Node runtime on the host). Loopback-only by default; non-loopback binds require `--allow-remote`/`AHA_ALLOW_REMOTE=1`. Hardened with Host-header allowlisting (anti-DNS-rebinding), JSON Content-Type enforcement on POST routes, a strict Content-Security-Policy, and a per-request timeout.
- Typed TypeScript client at `clients/typescript/` for code-mode runtimes (Cloudflare `@cloudflare/codemode`, Anthropic code-execution-with-MCP): generated typed surface (`aha-mcp.ts`) plus hand-written stdio and HTTP transports, a `tsconfig`, and runtime tests. Generated by `cmd/aha-gen-ts`; a Go drift test keeps the checked-in file in sync.
- `scripts/verify.sh ts` (and `make verify-ts`): typecheck and runtime-test the TypeScript client; skips gracefully when no TypeScript toolchain is present. `make gen-ts` regenerates the surface.
- `scripts/verify.sh mcp`: bidirectional cross-SDK conformance suite against the official Python, TypeScript, and Go MCP SDKs (three server legs + three client legs + a Code Mode workflow test + an in-process HTTP↔MCP consistency test). Skips gracefully when an SDK toolchain isn't installed.

- R2/local depot model for durable bundle storage.
- `aha depot init|ls|verify`, including `depot verify --repair`.
- `--depot` for `snapshot`, `refresh`, `ingest`, `status`, and `doctor`.
- Local depot default at `~/.aha/depot`.
- Content-addressed bundle layout: `bundles/v1/<bundle_sha256>.tar.zst`.
- Per-machine repairable catalog shards: `catalog/v1/<machine>.json`.
- R2 support through the AWS SDK S3 client with Cloudflare endpoint/region handling.
- Fake-S3 R2 tests plus a real-R2 integration test behind the `integration` build tag.
- Depot-specific fuzz targets for address parsing and bundle-key validation.
- Unified verification entrypoints via `scripts/verify.sh` and Make targets, including bounded fuzz and optional mutation profiles.
- `rapid` property-based testing dependency plus shared test generators for shrinkable invariant tests.
- Static correctness-debt inventory tests for ambient time, raw identity concatenation, manual FTS writes, and direct append-only table mutations.
- Correctness-by-construction guardrails and implementation hardening: typed identity/ref primitives, sealed canonical-only ref variants/parsing/formatting, corpus/depot state-machine skeletons, adapter conformance fixtures, schema introspection helpers, exact canonical read/human resolution split, clock/sleeper/backoff/source-capability seams, corpus verifier/reconciler queries, v2-only bundle schema validation, v2-only session-key construction, targeted SQLite foreign keys/checks, append-only/quarantine triggers, and trigger-maintained FTS rows.
- `status --depot` behind-bundle reporting.
- `doctor --depot` depot diagnostics, including common R2/S3 configuration mistake warnings.
- Refresh idempotency for unchanged source state.
- Property-based coverage for depot contracts, catalog merges, pending-ingest sets, archive path safety, config round trips, source path safety, and search/read coherence.
- `aha verify` for corpus invariant checks, with `--repair-fts` for derived FTS row repair.
- Prior-art improvement and performance audit specs documenting hardening requirements, hotspots, benchmark plans, and optimization guardrails.
- Optional command-level Go pprof output via `--cpuprofile`, `--memprofile`, `AHA_CPU_PROFILE`, and `AHA_MEM_PROFILE`.
- Pathological performance benchmarks for many tiny archive files, large ingest/verify/search/status corpora, and large local depot catalogs.
- Performance scalability/longevity plan tying benchmark/profiling findings to concrete optimization phases.
- Rowid-based FTS verification with query-plan guard, reducing pathological 5k-message verify from seconds to milliseconds.
- Known bundle identity handoffs: `archive.WriteWithInfo`, catalog `state_sha256`/`manifest_sha256`, depot known-SHA publish, and expected-SHA ingest staging.
- Quick/default depot verification with `--deep` for byte/manifests checks; fake-R2 coverage ensures quick verify does not download bundles.
- Ingest performance contracts: prepared statement lifecycle, session-local duplicate/conflict prefetch, covering conflict-query index, known file-blob no-recompression, duplicate-bundle parse-skip guard, and file-blob zstd encoder pooling.
- Indexed search filters: `--project` exact filter, `--path-token` path-segment filter for sessions/artifacts, actual query-plan guards, artifact coverage, and high-limit capping/warnings.
- Corpus maintenance commands: `aha corpus size`, `aha corpus vacuum`, and dry-run/forced `aha corpus prune-orphans`.
- Depot catalog compaction via `aha depot compact`, plus map-backed `MergeBundleRefs` properties and local/R2 compaction tests.
- Verify/status/depot-ingest cost counters for depot bytes read/downloaded, listed/unique depot refs, fetched pending bundles, corpus verify row counts, and FTS repair rows.
- Duplication-refactor pass sharing depot local/R2 integrity helpers, snapshot flag registration, search predicates, FTS predicates, and atomic file-write helpers, with before/after metrics and Go best-practices audit.

### Changed

- `snapshot` and `refresh` write to the configured depot instead of an output directory.
- No-argument `ingest` pulls pending depot bundles (`catalog - corpus`) instead of globbing an output directory.
- Trust docs now distinguish local-by-default behavior from explicit remote/R2 upload behavior.
- Generated command docs now include depot commands, depot-aware flags, corpus verification/maintenance, indexed search filters, global profiling guidance, depot quick/deep verification flags, and depot compaction.

### Removed

- Removed pre-release `--out` support.
- Removed pre-release `bundle_out_dir` config support.
- Removed local `.receipt.json` sidecars; bundle path/key and SHA are reported through command output/JSON, while durable metadata lives in the manifest and depot catalog.

### Security / correctness

- Depot ingest verifies catalog SHA/key against actual bundle bytes before corpus ingest.
- Local depot catalog updates use locking plus atomic writes.
- R2 catalog updates use conditional writes and retry-on-conflict behavior.
- `depot verify` is quick by default and validates marker/catalog/object-existence metadata; `depot verify --deep` validates bundle bytes/manifests and catalog/object agreement.
- Local depot paths and catalog-derived bundle keys are validated against traversal and malformed-key attacks.
- Network imports are restricted to `internal/depot` by static tests.

### Documentation

- Added architecture docs with aggregation/deduplication walkthroughs, generated command docs, trust documentation updates, R2 bucket-settings guidance, R2 snapshot aggregation spec updates, verification guidance, a corrected correctness-by-construction spec, performance result captures, refactor metrics, and docs/code-duplication audits.
- Added OpenCode research and evaluation docs.
