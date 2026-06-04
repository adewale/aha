# Cross-agent data capture, tree preservation, and redaction follow-ups

Research notes captured 2026-06. Sister to `docs/research/pi-observability.md`,
`docs/research/openinference.md`, and `docs/redaction-spec.md`.

The question this note answers: **are we losing data on ingest, and if so,
which data, from which source, and what should the v1.x roadmap do about
it?**

Three lenses:

1. *Field coverage* — what's in the raw source vs what we project into
   normalized columns.
2. *Structural fidelity* — Pi's tree, Claude's parent-uuid chains, Codex's
   thread links, subagent linkage.
3. *Redaction completeness* — gaps in `docs/redaction-spec.md` against the
   prior art from pi-share-hf.

## Current capture: shared parser baseline

All three adapters share `internal/adapters/parser.go:parseGenericJSONL`.
Per-entry projections it computes today (`parser.go:51-76`):

| Normalized column         | Source                                                                                                |
| ------------------------- | ----------------------------------------------------------------------------------------------------- |
| `entry_id`                | `id` ∨ `uuid` ∨ `message.id` ∨ `message.uuid` ∨ `line-<n>-<sha>` fallback                           |
| `parent_id`               | `parentId` ∨ `parent_id` ∨ `parentUuid`                                                              |
| `entry_type`              | `type`                                                                                                |
| `timestamp`               | `timestamp`                                                                                           |
| `role`                    | `role` ∨ `message.role` ∨ `type` fallback                                                            |
| `text`                    | walk of `message.content[]` for `text` / `tool_use` blocks (`parser.go:82-136`); `tool_result` skipped |
| `tool_name`               | `tool_use.name` ∨ `toolName` ∨ `message.toolName`                                                    |
| `command`                 | `tool_use.input.command`                                                                              |
| `files_json`              | full `tool_use.input` JSON                                                                            |
| `model`                   | `message.model` ∨ `model`                                                                            |
| `tokens`                  | sum of `message.usage.{input,output,cache_creation_input,cache_read_input}_tokens`                    |
| `raw_json`                | the original line, verbatim                                                                           |

What's deliberately **not** projected today:

- `tool_result` block contents (`parser.go:121-122` — "Preserved raw, not
  indexed in v1"). Recoverable from `raw_json` via `aha read`, but not FTS-
  searchable.
- `provider` (column exists in `messages`, never populated by any adapter).
- `cost` (column exists, never populated; per-model lookup is in the
  openinference impact estimate, not yet built).
- Cache-token breakdown (`cache_creation_input_tokens` is summed into
  `tokens`, then thrown away — we lose the read/write split that
  OpenInference's `llm.token_count.prompt_details.cache_{read,write}`
  explicitly tracks).
- Reasoning tokens (`message.usage.reasoning_tokens` is not parsed at all).
- Mid-session state transitions (`model_change`, `thinking_level_change`,
  `compaction`, `branch_summary`, `session_info`, `label`) flow through as
  normal entries; their typed fields are only searchable via `LIKE` against
  `raw_json`.

## Per-source structural fidelity

What each source provides natively vs what aha preserves:

| Capability                  | Pi (canonical) | Pi (aha today) | Claude Code (canonical) | Claude (aha today) | Codex (canonical) | Codex (aha today) |
| --------------------------- | -------------- | -------------- | ----------------------- | ------------------ | ----------------- | ----------------- |
| Stable per-entry IDs        | Yes (8-hex `id`) | Yes        | No (random `uuid`)      | Yes, but unstable across re-runs | No | Yes, but unstable |
| Parent linkage              | Yes (`parentId`) | Yes (`parent_id`) | Yes (`parentUuid`)  | Yes (`parent_id`)  | Partial (`parent_id` / `parentUuid` when present) | Yes when present |
| Alternate-timeline branches | Yes (siblings of one parent = branches) | **No — flattened on read** | No (single-thread) | n/a | Limited (fork-style) | **No — flattened on read** |
| Subagent hierarchy          | Yes (`parentSession` + filename hints) | Partial (`is_subagent` flag set; `parent_session_key` populated when adapter can infer) | Yes (`agent-*` filename prefix, subagent JSONL) | Flag-only (`CanLinkSubagents: false`) | Yes (rollout `parent`) | Flag-only (`CanLinkSubagents: false`) |
| Mid-session model swaps     | First-class entry | Lands in `entries.raw_json` only | Embedded in `message.model` per call | `messages.model` populated per row | Embedded per call | `messages.model` populated per row |
| Mid-session reasoning swaps | First-class (`thinking_level_change`) | `entries.raw_json` only | n/a | n/a | n/a | n/a |
| Compaction / context rewrite | First-class (`compaction` with `firstKeptEntryId`) | Stored as message with summary text; the context-rewrite semantics are dropped | Yes (summary entries) | Stored as message with summary text | Yes (summary entries) | Stored as message with summary text |
| User-applied labels         | Yes (`label` entry pointing at `targetId`) | `entries.raw_json` only | No | n/a | No | n/a |
| Extension state vs context  | Yes (`custom` vs `custom_message`) | Not distinguished | n/a | n/a | n/a | n/a |
| Tool-call ↔ tool-result link | `parentId` from result to call | Preserved in `parent_id`; not exposed in read API | `parentUuid` from result to call | Preserved in `parent_id`; not exposed | `parent_id` from result to call | Preserved; not exposed |
| Tool-result body indexed    | Yes in source | **No** (skipped by parser:121) | Yes in source | **No** | Yes in source | **No** |

The pattern is consistent: the *parse* preserves more than the *read* and
the *projection* preserves more than the *search*. We rarely *drop* data
during ingest; we drop it on every layer above.

## Pi-native projection: a discovered gap

The 2026-06 audit of the pi-mono corpus surfaced a category of data loss
not predicted by reading `parser.go` alone. The shared
`parseGenericJSONL` is tuned for **Anthropic / Claude Code**'s naming
conventions; Pi uses its own, and the parser silently drops several
projection surfaces for Pi sessions:

| Pi-native shape                                    | What aha does today                                                  |
| -------------------------------------------------- | -------------------------------------------------------------------- |
| `content[].type = "toolCall"` + `arguments: {...}` | Falls through `default` case (`parser.go:123`); no `tool_name`/`command`/`files_json` projection. |
| `content[].type = "thinking"` + `thinking` body    | Falls through `default`; thinking content is not joined into `messages.text`. |
| `content[].type = "image"` + top-level `data` / `mimeType` (no `source.*`) | `parser.parseImageAsset` reads `source.media_type` / `source.data`; Pi images are not detected as assets. |
| `message.usage.{input, output, cacheRead, cacheWrite, totalTokens}` (camelCase) | Token sum reads `input_tokens`, `output_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens` (`parser.go:72`). Pi token totals stay 0. |
| `message.usage.cost.{input, output, cacheRead, cacheWrite, total}` | `messages.cost` column exists, no adapter populates it. Pi already has the values in source. |
| `message.{api, provider, model, stopReason, responseId, toolCallId, exitCode, errorMessage, isError, cancelled, fullOutputPath, excludeFromContext, command, output}` | All raw_json-only. `message.model` happens to coincide with the Anthropic-style nesting and reaches `messages.model`; the rest are dropped from projection. |
| `message.details.{diff, firstChangedLine, truncation.*}` | Rich edit/truncation annotations that would feed pathology detection — all raw_json-only today. |
| Pi state-transition entries (`model_change`, `thinking_level_change`, `compaction`, `branch_summary`, `custom`, `custom_message`, `label`, `session_info`) with payload keys (`modelId`, `provider`, `thinkingLevel`, `firstKeptEntryId`, `tokensBefore`, `fromId`, `name`, `excludeFromContext`) | Stored as plain messages; payload keys are raw_json-only. Already itemized as priorities 3 and 4. |

**Status: fixed.** `projectPiNativeShapes` in `adapters/pi.go` now runs
after `parseGenericJSONL` and fills the Pi-shaped columns: `toolCall`
blocks → `tool_name`/`command`/`files_json`, `thinking` blocks →
`text` (prepended so reasoning is searchable), and the camelCase
`usage` schema (`input`/`output`/`cacheRead`/`cacheWrite`/`totalTokens`
plus `cost.total`) → the token and cost columns. Pinned by
`TestPiNativeProjectionSynthetic` and a real-corpus assertion
(`TestPiNativeProjectionRealCorpus`). The corresponding
`projection-table.json` rows have moved from `raw_only` to
`projected`. Per-category cost splits (`cost.input`/`output`/…) remain
`raw_only` pending dedicated columns.

This gap is invisible to anyone reading `parser.go` against the
canonical `session-format.md` because the canonical spec describes
**entry types** (`message`, `compaction`, …) while the data-loss is
inside the `message.content` block-type space (`toolCall`, `thinking`,
Pi-image). Running the corpus audit was the only way to find it.

## Codex-native projection: a second discovered gap

The same corpus audit applied to `cfahlgren1/codex-sessions` surfaced
that **Codex CLI's modern rollout format** wraps every entry in a
`payload` envelope instead of the `message` envelope our parser
assumes. A modern Codex rollout entry looks like:

```json
{"type":"response_item","timestamp":"...","payload":{"type":"agent_message","content":[{"type":"text","text":"..."}],"model":"gpt-5-codex","role":"assistant"}}
```

Our `parseGenericJSONL` reads `message.role`, `message.model`,
`message.content[].*` and `message.usage.*`. None of these are
populated for modern Codex rollouts, because the data lives under
`payload.*`. Concretely, for a modern Codex rollout the corpus today
produces:

- `entries.role` = "" (parser fell back to `entry_type` since
  `role`/`message.role` are absent).
- `messages.text` = "" (the parser walked `message.content`, which is
  absent).
- `messages.model` = "" (same).
- `messages.tokens` = 0 (Codex emits `payload.info.last_token_usage.*`
  and `payload.info.total_token_usage.*`; the parser sums Anthropic
  `input_tokens` / `output_tokens`).
- `messages.tool_name` / `messages.command` / `messages.files_json` =
  "" (function calls are `payload.type = "function_call"` with
  `payload.name` and `payload.arguments`; the parser handles
  `tool_use` blocks under `message.content[]`, not function-call
  entries).

The Codex `payload.type` discriminates `session_meta`, `user_input`,
`agent_message`, `function_call`, `function_call_output`,
`reasoning`, `rate_limits`, and `token_count`. None of these route
through projection today.

**Status: fixed.** `projectCodexPayloadEnvelope` in `adapters/codex.go`
runs after the generic parse, detects the `payload` envelope, and
populates `role` (from `payload.role` and `user_message`/`agent_message`
inner types), `text` (from `payload.content[].text`, `payload.message`,
`payload.output`), `model`/`provider` (carried forward from
`turn_context`/`session_meta`), `tool_name`/`command`/`files_json`
(from `function_call` and `web_search_call`), and the token columns
(from `payload.info.last_token_usage.*`). Pinned by
`TestCodexNativeProjectionSynthetic` and a real-corpus assertion
(`TestCodexNativeProjectionRealCorpus`) that fails if any vendored
rollout yields zero user/assistant/text entries. The pass is a no-op
for legacy `message`-envelope Codex sessions.

Together the two native-projection layers realise the shape this note
originally proposed: each adapter detects its source-native envelope
(`message.*` for Anthropic/Claude Code, `payload.*` for Codex modern,
`content[].type = "toolCall"` and camelCase usage for Pi) and routes
into the shared typed-column projections, while the generic
Anthropic-shaped path stays untouched for the formats it already
handles.

## Where we lose data, ranked

1. **Pi alternate-timeline branches (high impact, lossless-on-disk).**
   The parent tree is on disk in `entries.parent_id`; `aha read` just
   doesn't walk it. A `--branch leaf` flag on `read` plus a
   `read --tree session:v1:...` dump would close this without a schema
   change.
2. **Compaction semantics (high impact, lossy today).** Reconstructing
   "what the model actually saw at turn 47" requires walking compaction
   nodes; we don't. The schema needs to capture `compaction.firstKeptEntryId`
   and `compaction.tokensBefore` so a future replay tool can compute the
   live context window.
3. **Tool-result body FTS (medium impact, controlled by a flag).** The
   `index_tool_output: false` default means `Bash` stdout, `Read` output,
   and grep results are not searchable. That's defensible (size, cardinality,
   secrets) but the spec/README don't even acknowledge it as a search gap.
   With redaction in place (`v1.1`), flipping it on becomes safer.
4. **Cache and reasoning token breakdown (medium impact, lossy today).**
   We sum, then discard, the cache-read/cache-write split. OpenInference
   already has the right column names; adding them now is essentially free
   storage-wise.
5. **Pi `model_change` / `thinking_level_change` / `label` (low-to-medium
   impact, lossless-on-disk).** Visible only via `LIKE` against
   `entries.raw_json` today. Adding dedicated `messages.thinking_level`
   and `messages.label_target` columns is one migration.
6. **Subagent linkage for Claude and Codex (low impact, hard to fix at
   source).** `CanLinkSubagents: false` is honest — the data isn't in the
   source. No adapter change can synthesize it. Leave as a documented
   limit.
7. **Provider and cost (low impact, additive).** Both columns exist
   unpopulated. Per-model price tables and provider inference are not in
   the source; we'd compute them. Worth a flag, not a default.

## Tree-preservation strategy

Three coordinated changes, smallest first:

### A. Read API: walk the tree (no schema change)

- `aha read <session-ref> --tree` — emit every entry in tree order
  (depth-first by `parent_id`), tagging siblings as `branch=A | branch=B`.
- `aha read <session-ref> --branch <leaf-entry-id>` — emit only the
  leaf→root path. This is the natural "what was the live context" view for
  Pi and a no-op for Claude/Codex (single-thread; leaf = last entry).
- `aha search ... --branch leaf` — restrict FTS hits to live-context
  entries on the most-recent leaf.

Cost: ~200 LOC plus a property test that walks every Pi fixture and
asserts the leaf→root path matches Pi's `buildSessionContext()` output.

### B. Compaction-aware context reconstruction (small schema change)

Add three sparse columns to `messages`:

- `compaction_first_kept_entry_id` (TEXT, nullable)
- `compaction_tokens_before` (INTEGER, nullable)
- `participates_in_context` (BOOLEAN, default true) — false for Pi
  `custom` and Claude tool-meta entries.

A new helper `corpus.LiveContext(sessionKey, leafEntryID)` walks
leaf → root, skips compacted-out entries based on `first_kept_entry_id`,
and skips entries with `participates_in_context = false`. This is the
projection an LLM-replay or "what did the model see" tool would want.

Cost: ~50 LOC for the migration + ~150 LOC for the walker + property tests
against the pi-mono dataset.

### C. Typed state-transition columns (small schema change)

Add to `messages`:

- `thinking_level` (TEXT, nullable) — populated by `thinking_level_change`
  entries and inherited forward by the context-walker.
- `label` (TEXT, nullable) and `label_target_entry_id` (TEXT, nullable)
  for Pi labels.
- `provider` (TEXT, already in schema; populate from `model_change` and
  per-message hints).

Cost: ~100 LOC for the adapter changes + per-source fixture updates.

## Redaction proposal updates

`docs/redaction-spec.md` v1.1 covers the deterministic-pattern pass cleanly.
pi-share-hf adds three orthogonal layers on top:

### v1.2: exact-secret redaction

Before pattern matching, redact known exact secrets:

- Read `.env`, `.envrc`, `.env.local`, `.env.<environment>` from the
  session's `cwd` (or the bundle's preserved working copy, if we keep one).
- Build a literal-match table of `(key, value)` pairs whose value is
  ≥ 8 chars and not in a curated allow-list (`PORT=3000`, `DEBUG=true`).
- Run a literal-string pass *before* the regex pass; replace with
  `[REDACTED:env:<key>]`.

This is strictly better than regex for secrets that exist on disk: it can't
generate false positives, and it labels the exact env var.

Schema delta:
```sql
alter table sessions add column exact_redactions_count integer default 0;
```

Surface: `aha status` adds `exact_redactions_total`; `aha doctor` reports
which env files contributed.

### v1.3: second-opinion scanner (gated)

Add a hook: if `config.redaction_second_opinion = "trufflehog"` (or
`"detect-secrets"`), shell out post-redaction to the configured tool over
the redacted text. Any non-empty result aborts the ingest of the affected
session with a diagnostic. Default is `"none"`; this is opt-in for the
paranoid.

Why post-redaction and gated: TruffleHog has its own false-positive rate
and dependency footprint, so we run it as a *check on our work*, not as a
mandatory pipeline stage.

Schema delta:
```sql
alter table sessions add column second_opinion_status text default 'unscanned';
-- "unscanned" | "clean" | "flagged"
```

### v1.4: LLM review gate (opt-in, per-session)

For users who want pi-share-hf parity:

- A new command `aha review --session <ref> [--model claude-3-5-haiku]`
  asks an LLM three questions over the redacted session: is this safely
  shareable, is anything project-irrelevant, did the redactor miss
  anything?
- Record the review verdict in a `session_reviews` table; `aha snapshot
  --review-required` refuses to publish a bundle whose sessions haven't
  passed review.

This is a sharing-flow feature, not an ingest feature, and lives behind a
flag. We do not run LLMs during normal ingest.

### v1.5: per-session audit trail

Mirror pi-share-hf's workspace layout for shared depots:

- For every published session, the depot also stores the raw redactor
  output, the raw second-opinion report, the LLM review (if any), and the
  per-session pattern-hit counts.
- Existing redaction observability (`status`, `verify`) already gives the
  *aggregate* numbers; this gives the *evidence trail* a teammate or
  auditor can replay.

## Recommended ordering

Priority is set by "how much data are we losing today" × "how much work":

1. (Tree walk on `read`) — no schema change, ~200 LOC. Biggest lossless-
   on-disk win.
2. (Cache + reasoning token columns) — schema only adds nullable columns;
   already on disk in Claude transcripts; OpenInference impact estimate
   already costed this at <1 person-week.
3. (Compaction-aware context reconstruction) — unlocks faithful
   "what-the-model-saw" replays.
4. (Typed state-transition columns) — Pi gets first-class state queries;
   Claude/Codex unaffected.
5. (Exact-secret redaction, redaction v1.2) — strictly safer than regex
   for env-file values.
6. (Index tool_result content, flag-gated) — once redaction v1.1 lands.
7. (Second-opinion scanner v1.3) — opt-in.
8. (LLM review gate v1.4) — opt-in, sharing-flow only.
9. (Per-session audit trail v1.5) — depot-only, deferred.

Items 1–3 should land before redaction v1.2 because faithful reconstruction
must be in place before we can confidently say a redacted bundle preserves
"what the model actually saw." Items 5–9 build on the pi-share-hf prior
art for sharing-grade safety; they should not block local-only ingest.

## File references

In this repo:
- `internal/adapters/parser.go` — shared JSONL parser.
- `internal/adapters/pi.go`, `claude.go`, `codex.go` — per-source
  adapters and capability flags.
- `internal/corpus/schema.go` — corpus shape.
- `docs/redaction-spec.md` — v1.1 baseline.
- `docs/research/pi-observability.md` — Pi schema + pi-share-hf pipeline.
- `docs/research/openinference.md` — OpenInference naming target.
- `docs/research/openinference-impact-estimate.md` — cost model for the
  cache/reasoning token columns.
