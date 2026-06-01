# OpenInference adoption: data-size and performance impact

Companion to `docs/research/openinference.md`. Quantifies what changes if
aha aligns with the OpenInference (OTel-extending) semantic conventions for
LLM / tool / agent spans.

The estimates are rough but bounded: where assumptions are uncertain we
state them in line so the numbers can be revisited.

## Two adoption levels

We use two phases throughout. They are independent choices.

| Phase | Adopt                                                                                                   | Skip                                                                                            |
| ----- | ------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------- |
| **1** | OpenInference attribute names (cache/reasoning tokens, span kinds, tool fields), schema migration, OTLP export bridge | Verbatim `input.value` / `output.value`; flattened `llm.input_messages.*`; full agent-trace export |
| **2** | Everything in Phase 1 plus verbatim payloads and flattened message arrays                              | —                                                                                                |

Phase 1 is small, additive, and a clear win. Phase 2 is *expensive* and is
only worthwhile if aha wants to be an OTLP-native source for Phoenix or
Langfuse.

## Current schema baseline

Sizes are rough but representative. Source: `internal/corpus/schema.go` and
typical Claude Code / Codex session bytes.

Per-entry rows aha writes today:

| Table         | Fields touched                                                              | Bytes per row (typical) |
| ------------- | --------------------------------------------------------------------------- | ----------------------- |
| `entries`     | `raw_json` (≈2 KB), `source_metadata_json` (≈0.3 KB), plus keys             | ~3 000                  |
| `messages`    | `text` (≈1 KB), `tool_name`/`command`/`files_json`/`model`/`tokens`/`cost`  | ~1 000                  |
| `entry_assets`| Sparse — only for messages with attachments                                 | ~200 (when present)     |
| FTS row       | Mirrors `messages.text`                                                     | ~1 000                  |
| **Total per entry** |                                                                       | **~4–5 KB**             |

For a typical 100-entry coding-agent session: ~400–500 KB on disk. For a
user running 10 sessions/day, 100 entries/session: ~4–5 MB/day,
~1.5–1.8 GB/year before any indexing overhead. SQLite WAL plus the FTS
auxiliary tables add roughly another 30 %.

## Phase 1 estimate — adopt the OpenInference taxonomy

This is what we recommend.

### Schema changes

Add columns to `messages` (sparse — `null` for entries that don't have an
LLM/tool span):

| Column                         | Type    | Bytes per row | Source                                                              |
| ------------------------------ | ------- | ------------- | ------------------------------------------------------------------- |
| `openinference_span_kind`      | TEXT    | 12–20         | Constant per row: `LLM`, `TOOL`, `AGENT`, `CHAIN`, …                |
| `llm_invocation_parameters`    | TEXT    | 80–300        | JSON of temperature/top_p/max_tokens; ≈100 chars typical            |
| `cache_read_tokens`            | INTEGER | 8             | Already on disk in Claude `cache_read_input_tokens`                 |
| `cache_write_tokens`           | INTEGER | 8             | Already on disk in Claude `cache_creation_input_tokens`             |
| `reasoning_tokens`             | INTEGER | 8             | Currently ignored                                                   |
| `tool_description`             | TEXT    | 100–500       | Sparse — only for `TOOL` spans                                      |
| `tool_json_schema`             | TEXT    | 200–1 000     | Sparse, often deduplicatable per tool                               |
| `cost_prompt` / `cost_completion` / `cost_total` | REAL × 3 | 24 | Per-model price lookup, already derivable        |

Estimated net growth per `messages` row:

- LLM-call rows: **+150 bytes** (parameters + token columns).
- Tool-call rows: **+400 bytes** (description + schema; partially offset by deduplicating schema across calls).
- Other rows: **+30 bytes** (just span kind + null columns).

For a typical session at the mix observed in Claude transcripts (~30 % LLM,
~40 % tool, ~30 % other): average **~+200 bytes per row**, or **~+1 KB per
entry** including the FTS overhead. That's roughly a **10–15 % corpus
growth**.

For a 10 sessions/day, 100 entries/session user:
- Current: ~1.5 GB/year
- Phase 1: ~1.65–1.75 GB/year

Acceptable. Storage growth is dwarfed by the size of the bundles themselves
(which are unchanged).

### Performance — ingest

- Extra column writes per insert: 8 columns. SQLite handles this in the
  same statement; expected ingest slowdown is **<5 %**.
- Extra JSON parsing during normalization (extract `tools` array,
  `invocation_parameters`): ~2 ms per LLM-call entry; below the noise floor
  of bundle extraction (~10 ms per entry).
- Net ingest impact: **<10 %** on the worst-case dimension; likely
  imperceptible end-to-end.

### Performance — query

- FTS index unchanged (still over `messages.text`).
- New columns don't get FTS-indexed. They serve targeted SQL queries
  ("show me sessions where total cost > $1", "find tool calls to Bash with
  cache_read > 0").
- Index additions: one on `messages(openinference_span_kind)` for filtering
  by kind. Adds ~5 % to ingest, well within budget.
- Read API unchanged. MCP `read` and dashboard `/api/read` return the same
  shapes; new fields are nullable and additive.

### Performance — export

- A new `aha export --otel` command can stream OTLP spans built from the
  enriched `messages` rows. Throughput estimate: ~5 000 spans/sec on a
  laptop (limited by JSON marshalling, not SQLite). Negligible storage —
  it's a stream, not a persisted artifact.

### Engineering cost

- Schema migration: ~50 LOC + property test for backfill.
- Adapter changes (Claude / Codex / Pi): ~200 LOC each, with conformance
  fixtures.
- Fuzz target for the OpenInference normalizer: ~80 LOC.
- OTLP export bridge: ~600 LOC for protobuf encoding + HTTP/gRPC client.
- Total: **~1–2 person-weeks** including tests.

### Recommendation

**Do it.** The cost is low, the data is already on disk in the source
JSONL, and the payoff is real: targeted pathology queries
(`tool_loop`, `cache_miss_rate`, `cost_per_session`), interop with
Phoenix/Langfuse via OTLP, and a clear taxonomy that maps to the
cross-tool vocabulary the ecosystem is converging on.

## Phase 2 estimate — verbatim payloads and flattened messages

This is what we recommend *against*, with caveats.

### Schema changes

| Column                                       | Type | Bytes per row (typical) | Why expensive                                                    |
| -------------------------------------------- | ---- | ----------------------- | ---------------------------------------------------------------- |
| `input_value`                                | TEXT | 5 000 – 50 000          | Full prompt JSON; grows with conversation history                |
| `output_value`                               | TEXT | 1 000 – 10 000          | Full completion JSON                                             |
| `input_messages_flat` (1 row per turn)       | TEXT | 2 000 – 20 000          | Redundant with `input_value`; OTel attribute-map flat encoding   |
| `output_messages_flat` (1 row per turn)      | TEXT | 1 000 – 5 000           | Redundant with `output_value`                                    |
| `retrieval_documents_flat` (sparse, retrievers) | TEXT | 1 000 – 100 000      | Verbatim document content; OpenInference stores it raw           |

Estimated net growth per LLM-call row: **+10–80 KB per row at typical
sizes**, growing without bound for long histories or retrieval-heavy
sessions.

The killer is conversation history. By turn 50 of a Claude Code session,
the `input.value` for the 51st turn contains the full transcript of the
previous 50 turns — and we'd be storing that *every turn*. The on-disk
JSONL doesn't have this problem because each entry stores only the *delta*;
OpenInference flatness forces aha to re-serialize the entire context per
turn.

For our 10-sessions × 100-entries example:
- Phase 2 estimate: **40–200 MB/day**, **15–75 GB/year**.

This is 10–50× Phase 1 storage. Disk cost alone disqualifies it for a
local-first tool.

### Performance — ingest

- Per-row write goes from ~5 KB to ~50–500 KB.
- SQLite append throughput on a single connection: ~150 MB/s sequential
  for large rows. For a 100-entry session with 50 KB per row, ingest goes
  from ~50 ms to ~3 s — **30–60× slower**.
- JSON serialization cost: ~100 ms per session, adds another ~10 % on top.

### Performance — query

- FTS over `input_value` would index full transcripts and explode the
  index size by ~3×. Skip it.
- Targeted queries on `input_value` use SQL `LIKE` and are slow (no
  index): ~100 ms per query on a 1 GB corpus.
- Read API payloads grow proportionally: a single `read` could return
  multi-MB JSON. Dashboard would need pagination or aggressive truncation
  on the wire.

### When Phase 2 makes sense

Only if aha takes on the role of being an **OTLP source** for an external
backend (Phoenix, Langfuse, Jaeger). In that case the verbatim payloads
are required by the spec, but they should be **streamed at export time,
not persisted in aha's own corpus**. The right design is:

- aha persists Phase 1 schema (enriched but compact).
- `aha export --otel --session <id>` reconstructs verbatim `input.value`
  from the *bundle* (which already has the raw transcript) at export time,
  rather than duplicating it in SQLite.

This keeps aha's storage small while still enabling OTLP-native export.

### Recommendation

**Skip the verbatim columns. Stream them on export.** The bundle already
has the source; duplicating it in the corpus turns a 1.5 GB/year archive
into a 50 GB/year archive for no read-path benefit.

## Summary

| Dimension                     | Phase 1                      | Phase 2 (verbatim payloads)   |
| ----------------------------- | ---------------------------- | ----------------------------- |
| Storage growth                | +10–15 %                     | +10–50×                       |
| Ingest slowdown               | <10 %                        | 30–60×                        |
| Read-path impact              | None                         | 5–10× larger payloads          |
| OTLP export                   | Possible via bridge          | Native                        |
| Pathology queries             | Cheap SQL                    | Same                          |
| Engineering cost              | 1–2 person-weeks             | +3–4 person-weeks              |
| Recommendation                | **Adopt**                    | **Stream at export, don't persist** |

## Conclusion

Adopting OpenInference's **naming** and adding the high-value sparse columns
(cache/reasoning tokens, span kind, invocation parameters, tool fields) is a
clear win: small, additive, opens the door to Phoenix/Langfuse interop, and
matches the cross-tool taxonomy aha would already want for pathology
detection.

Adopting OpenInference's **storage shape verbatim** is not worth it for a
local-first tool. The verbatim payloads belong in OTLP export, generated on
demand from the bundle, not duplicated into the SQLite corpus.

aha's existing bundle/depot model is precisely what makes this split clean:
the source-of-truth payloads are already preserved in immutable
content-addressed bundles, and the corpus stays a *queryable projection*
rather than a full archive of every byte twice.
