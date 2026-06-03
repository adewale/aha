# Pi observability and the pi-mono dataset

Research notes captured 2026-06. Companion to `docs/research/openinference.md`
and `docs/research/agent-trace-tools.md`. Use as a reference when planning Pi
adapter improvements and the redaction spec.

## What it is

Three artifacts from Mario Zechner (`badlogic`), the author of Pi:

- **[`earendil-works/pi`](https://github.com/earendil-works/pi)** — the Pi
  monorepo. Packages: `@earendil-works/pi-coding-agent` (CLI),
  `@earendil-works/pi-agent-core` (runtime with tool calling and state
  management), `@earendil-works/pi-ai` (unified multi-provider LLM API),
  `@earendil-works/pi-tui` (terminal UI with differential rendering). Renamed
  from `badlogic/pi-mono`; aha's dataset card still points at the old path.
- **[`badlogic/pi-share-hf`](https://github.com/badlogic/pi-share-hf)** —
  Mario's tool for publishing redacted Pi sessions to Hugging Face datasets.
- **[`badlogicgames/pi-mono` on Hugging Face](https://huggingface.co/datasets/badlogicgames/pi-mono)** —
  627 redacted sessions, 2,325 downloads in the last month, JSONL.

Pi is already a supported aha source
(`~/.pi/agent/sessions/--<path>--/<timestamp>_<uuid>.jsonl`), so the schema
and prior art are directly load-bearing for our adapter and the v1.1
redaction spec.

## Mario's stance on observability

Observability is a *first-class* design constraint for Pi, not a debugging
afterthought. The themes that recur in his write-ups:

- Power users need to know exactly *what hits the model's context window*.
  Claude Code is explicitly criticised for "reducing visibility in service of
  UI simplicity."
- Cost tracking, HTML/JSON export, headless JSON streaming, and a
  **tree-structured session history** (not a linear chat list) are all
  present in Pi from day one.
- The OpenCode compaction strategy ("prune all tool results before the last
  40k tokens") is held up as a cautionary tale: it destroys Anthropic prompt
  caching, and you cannot diagnose that without seeing the full context
  history.

Source: ZenML LLMOps Database, *Building Pi: A Minimal, Extensible Coding
Agent Framework*.

## The canonical Pi session format

From
[`packages/coding-agent/docs/session-format.md`](https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/session-format.md).
Sessions are JSONL; **every line is a tree node** with `type`, `id`,
`parentId`, `timestamp`. The first line is a session header (no tree
participation); the rest form a parented tree.

| Type                      | Required fields                                            | Notes                                                                 |
| ------------------------- | ---------------------------------------------------------- | --------------------------------------------------------------------- |
| `session`                 | `version`, `id`, `timestamp`, `cwd`                        | Header. Optional `parentSession` for forks. Not part of the tree.     |
| `message`                 | `id`, `parentId`, `timestamp`, `message`                   | `message` is one of `UserMessage`, `AssistantMessage`, `ToolResultMessage`, `BashExecutionMessage`, `CustomMessage`, `BranchSummaryMessage`, `CompactionSummaryMessage`. |
| `model_change`            | `provider`, `modelId`                                      | Mid-session model swaps are first-class.                              |
| `thinking_level_change`   | `thinkingLevel`                                            | Reasoning-effort swaps are first-class.                               |
| `compaction`              | `summary`, `firstKeptEntryId`, `tokensBefore`              | Rewrites which earlier entries belong to live context.                |
| `branch_summary`          | `fromId`, `summary`                                        | Records a summary of an alternate branch.                             |
| `custom`                  | `customType`, `data`                                       | Extension state. **Does NOT** participate in LLM context.             |
| `custom_message`          | `customType`, `content`, `display`                         | Extension content that **DOES** participate in LLM context.           |
| `label`                   | `targetId`, `label`                                        | User-applied tag on another entry.                                    |
| `session_info`            | `name`                                                     | Renaming.                                                             |

**Tree semantics.** First non-header entry has `parentId: null`. Branching
creates alternate children of an earlier entry. The current position is
whichever leaf the user is "on." `buildSessionContext()` walks
leaf → root, splicing in compaction summaries from `firstKeptEntryId`
onward.

This is fundamentally **not the OTel "DAG of nested spans" model**:

- Edges are *conversational lineage* (which entry derives from which), not
  call-stack nesting.
- Sibling children of one parent are *alternate timelines*, not parallel
  sub-operations.
- Compaction is a *first-class entry that rewrites context*, not a
  downstream concern.
- `custom` vs `custom_message` is an explicit *participates-in-context* bit
  on the entry, not a tag.

OTel can model some of this with spans + `session.id`, but OpenInference's
`AGENT` span and OTel GenAI's `invoke_workflow` have no native concept of
*alternate-timeline siblings* or *context-rewriting nodes*. Pi's tree is
strictly more expressive for branching and forking.

## pi-share-hf: prior art for the redaction spec

The pipeline pi-share-hf uses to make sessions safe to publish:

1. **Collect** Pi session files from a project's `cwd`.
2. **Redact deterministically** — exact secrets from environment files and
   user inputs first, then curated credential patterns.
3. **Filter against deny patterns** — drop sessions that touch known-private
   paths regardless of content.
4. **TruffleHog scan** — third-party secret detector run over the redacted
   output to catch what the deterministic pass missed.
5. **LLM review** of remaining sessions, asked three questions:
   (a) is this session about the OSS project? (b) is it fit to share
   publicly? (c) does anything sensitive appear to have been missed?
6. **Upload only sessions that pass every check.** Default workspace
   `.pi/hf-sessions/` keeps redacted files, raw reports, LLM reviews, and
   extracted images side-by-side for auditability.

Where aha's current spec stops at step 2 (regex patterns) and step 6
(observable hit counts), pi-share-hf adds:

- **Exact-secret redaction first.** When a `.env` is known, redacting its
  literal values is strictly better than pattern matching.
- **A second-opinion scanner** (TruffleHog) as a defence in depth.
- **LLM review as gate, not gate-keeper.** A model is asked whether anything
  *looks* like a leak — not used to redact, but to *block* the share.
- **Per-session audit trail.** Redacted file + raw report + LLM review +
  image extracts are kept together. The user can answer "why was this
  session published?" months later.

## Implications for aha

1. **Preserve Pi's `parentId` tree.** Our Pi adapter already extracts
   `parentId` into `entries.parent_id` (`internal/adapters/parser.go:52`,
   `pi.go:23` advertises `HasThreads: true`), but `aha read` reconstructs
   in **file-order**, not tree-walk order (README line 274). The data is on
   disk; the read path drops it. A `read --branch leaf` option that walks
   leaf → root via `parent_id` would restore the lossless reconstruction Pi
   intends.
2. **First-class compaction handling.** A `compaction` entry rewrites which
   earlier entries belong to live context (`firstKeptEntryId`,
   `tokensBefore`). aha currently treats it as a normal message
   (`parser.go:74-76`). The reconstructed "live context" for an LLM-replay
   use case should respect compaction; otherwise we surface entries the
   model never actually saw at that turn.
3. **Capture `model_change` and `thinking_level_change` as projections, not
   message text.** These are first-class state transitions in Pi. Today
   they land in `messages.text` only if their JSON happens to have a
   `text`/`summary` field. They should drive a `messages.model` (already
   present) and a new `messages.thinking_level` column for direct querying
   ("what's my reasoning-effort distribution per session?").
4. **Capture `custom` vs `custom_message` distinction.** The bit "does this
   participate in LLM context?" is the single most important attribute on
   any Pi extension entry. Storing it would let `aha read --context-only`
   reconstruct exactly what the model saw.
5. **Adopt pi-share-hf's redaction pipeline as v1.2 extensions.** See
   `docs/redaction-spec.md` for the v1.1 baseline. Concrete additions:
   exact-secret redaction from `.env` files, optional second-opinion
   scanner (TruffleHog or `detect-secrets`) gating ingest, optional LLM
   review gate behind a config flag.
6. **The pi-mono dataset is a free conformance corpus.** 627 real sessions
   through the canonical Pi format is exactly the fixture set the Pi
   adapter's property and round-trip tests want. We should ingest a copy
   into `internal/adapters/testdata/pi-mono/` and run the property suite
   against the full set — both as a smoke test for the adapter and as a
   regression guard for the redaction spec.

## File references

In `earendil-works/pi`:
- `/packages/coding-agent/docs/session-format.md` — schema (this is the
  load-bearing one).
- `/packages/coding-agent/docs/sessions.md` — lifecycle, branches,
  `/tree`/`/fork`/`/clone` commands.
- `/packages/coding-agent/docs/compaction.md`, `extensions.md`,
  `models.md` — adjacent semantics.

In `badlogic/pi-share-hf`:
- `/README.md` — pipeline overview.

In this repo:
- `internal/adapters/pi.go` — Pi adapter capability flags and discovery.
- `internal/adapters/parser.go:22-80` — shared JSONL parsing.
- `internal/adapters/parser.go:74-76` — current compaction/summary handling.
- `internal/corpus/schema.go` — corpus shape, `parent_id` storage.
- `docs/redaction-spec.md` — v1.1 baseline to extend.
