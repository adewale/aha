# Trajectory interop spec — importer and exporter

Spec captured 2026-07. Sister to `docs/redaction-spec.md`,
`docs/correctness-by-construction-spec.md`, and
`docs/research/agent-trace-tools.md`.

Target: read and write Letta's `trajectory` interchange format so that (a)
any harness that can emit a trajectory becomes ingestible by aha without a
bespoke adapter, and (b) any aha Workspace can hand a normalised, redacted,
token-bounded session to an agent or to another tool in that ecosystem.

**Status: specification only. No implementation exists yet.** Every section
below describes work to be done under `agents.md` red-green-refactor
discipline.

## Provenance and what is authoritative

The blog post (<https://www.letta.com/blog/trajectory/>) is **not**
authoritative and its prose disagrees with the schema in ways that matter
(see "Six properties" below). The authoritative artefacts are:

| Artefact | Location | Role here |
| --- | --- | --- |
| `schema/trajectory-v1.schema.json` | `letta-ai/trajectory`, `$id` `https://letta.ai/schemas/trajectory/v1.json` | The interop contract. Vendored and gated on. |
| `schema/trajectory-canonical-v1.schema.json` | same repo, `$id` `.../canonical-v1.json` | Ingestion rows for Letta Cloud. Out of scope — see "Non-goals". |
| `@letta-ai/trajectory` | npm, v0.2.0, Apache-2.0 | Reference implementation for the opt-in differential test. |

The package is Apache-2.0, so the schema is vendored into
`internal/trajectory/testdata/schema/trajectory-v1.schema.json` **verbatim**,
with its licence and the upstream commit recorded in a sibling
`PROVENANCE.md`. A static test asserts the vendored bytes are unmodified
relative to that recorded hash; refreshing the schema is a deliberate commit,
never a drive-by edit.

## Naming: this repository already has a "trajectory"

`incident_trajectory` / `analyse_failure_trajectory` / `corpus.TrajectoryStep`
already mean *the fail-to-fix arc behind a resolved incident*
(`docs/outcome-weighting-spec.md`). That meaning is shipped in the MCP
contract, the HTTP routes, the dashboard, and the generated TypeScript
client, so it does not move.

Rules for the new work:

- the interchange format is always written `trajectory-v1` (or "the
  trajectory format") in prose, never bare `trajectory`;
- Go types live in `internal/trajectory` and are named for the format —
  `trajectory.Document`, `trajectory.Record` — never `Trajectory`;
- the CLI/config token is `trajectory-v1` for the format and `trajectory`
  for the *source type*, which is unambiguous because source types are a
  closed set;
- `internal/testquality/v02_vocabulary_test.go` gains a rule: any new
  occurrence of bare "trajectory" in `docs/` prose must be either
  `trajectory-v1`, "the trajectory format", or one of the frozen incident
  phrases. The allowlist starts at the current occurrences and only shrinks.

## Six properties of trajectory-v1 that drive our design

Read directly off the schema, because each one contradicts an assumption
someone would otherwise carry over from our JSONL adapters.

**1. A trajectory is a JSON array, not JSONL.** `"type": "array",
"minItems": 1`. Every existing aha adapter is line-oriented
(`discoverJSONL`, `parseGenericJSONL`, per-line `RawJSON`, the `line-%06d`
identity fallback). The importer therefore cannot reuse
`parseGenericJSONL`; it needs a streaming `json.Decoder` array walk. The
exporter cannot append lines; it must emit one well-formed array.

**2. `additionalProperties: false` on every variant.** There is no
extension point anywhere in the format. aha cannot smuggle entry IDs, refs,
machine IDs, token counts, or redaction hit counts into an exported record.
Everything aha wants to say about an export goes in a **sidecar** file, and
a strict validator remains the acceptance gate.

**3. The assistant record is a closed two-way variant.** From the `allOf`:
if `tool_calls` is present then `content` **must be null**; otherwise
`content` must be a string with `minLength: 1`. An assistant record that
carries both prose and tool calls is not representable, and neither is one
that carries neither. This is a sum type, and we model it as one.

**4. Tool results carry no error signal.** The `tool` variant is exactly
`{role, tool_call_id, content, timestamp}`. There is no `is_error`, no
`exit_code`, no duration, no token usage. Our `ParsedToolResult.IsError`,
`ExitCode`, and every `usage` field are **lost on export** and **absent on
import**. Import must not infer them — inventing an error flag from a
substring match would be a fabricated field in a durable Archive.

**5. Timestamps are pattern-constrained and mandatory on conversational
records.** `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})$`.
aha stores `ParsedEntry.Timestamp` as whatever string the source produced,
and several real sources produce entries with no timestamp at all. Export
must therefore have an explicit, loud policy for untimestamped entries
(below), not a silent drop.

**6. `meta` is required-ish, thin, and timestamp-free.** Required
`{role, source}`; optional `{cwd, git_branch, model}`. There is no session
ID, no parent ID, no subagent linkage, no branch structure, no compaction
marker, no per-step usage. The format is flat by construction.

## Non-goals

- **Not our internal model.** trajectory-v1 deletes precisely the fields
  `docs/research/cross-agent-data-capture.md` lists as gaps we intend to
  *close* (cache read/write split, reasoning tokens, cost, `tool_result`
  contents, exit codes). It is a projection out of `ParsedEntry`, never a
  replacement for it.
- **Not `trajectory-canonical-v1`.** That schema's own description says the
  Letta Cloud worker owns tenancy, raw-upload lineage, ingestion identity,
  `content_version`, `config_hash`, and the final ClickHouse
  `record_index`. It is an internal ingestion row format for one vendor's
  pipeline, not a neutral interchange target. Revisit only if a second
  independent producer adopts it. Its `source_identity_kind` **vocabulary**
  is the one exception: we adopt it as `EntryIdentityKind` (§Correctness by
  construction item 8), because it names a distinction we already make
  badly. Adopting four words is not adopting a format.
- **No replay.** We do not reconstruct a runnable session from a
  trajectory. Harbor's ATIF exists for full-fidelity replay; we are not
  competing with it.
- **No new network behaviour.** Import reads local files; export writes
  local files. Neither touches R2.

## Architecture

### The importer is a source adapter, and needs no new command

`internal/adapters/trajectory.go` implements the existing `SourceAdapter`
interface and joins `Builtins()` as `"trajectory"`. That single decision
buys, with no new code:

- redaction at the corpus boundary (`docs/redaction-spec.md` — the
  importer never has to think about secrets);
- content-addressed blobs, manifests, and per-file SHA-256 via
  `aha archive upload`;
- the read-only guarantee, enforced for free by `read_only_test.go`, which
  globs `*.go` in the package;
- conformance, fidelity, and key-path-coverage harnesses;
- cross-machine identity, conflict quarantine, and `workspace verify`.

Adapter shape:

```go
func (Trajectory) Name() string    { return "trajectory" }
func (Trajectory) Version() string { return "v1" }
func (Trajectory) DefaultRoots() []model.DefaultRoot { return nil }
func (Trajectory) Capabilities() model.AdapterCapabilities {
	return model.AdapterCapabilities{
		HasThreads: false, HasSubagents: false, HasImages: false,
		HasToolCalls: true, HasStableEntryIDs: false, CanLinkSubagents: false,
	}
}
```

`DefaultRoots()` is deliberately empty. Unlike `~/.claude/projects`, there
is no canonical on-disk home for trajectory documents — it is an
interchange format, so the root is always explicit opt-in in config. This
also means `aha init` never silently starts ingesting a directory a user
filled with exports.

Discovery accepts `*.json` (schema-conformant array documents) and
`*.jsonl` (one record per line — the reference tooling emits this shape
too). The two are distinguished by first non-whitespace byte, not by
extension, and a file whose extension and content disagree is a diagnostic,
not a parse attempt.

### The exporter is a projection package plus one Workspace transition

`internal/trajectory` holds a pure projection with no I/O:

```go
func Project(session Session, opts Options) (Document, Report, error)
```

`Session` is built only from redacted corpus rows (below). `Report` carries
what was dropped and why, and the truncation accounting.

**`export` cannot be a top-level command.**
`TestV02RegistryIsTheOnlyPublicCommandSurface` freezes the public surface at
`analyse, archive, dashboard, init, mcp, search, show, status, workspace`
and explicitly asserts that `export` returns "unknown command". Reviving it
would silently reverse a 0.2 decision. The transition therefore lands as a
Workspace operation:

```bash
aha workspace export --format trajectory-v1 --out DIR [--session ID] [--project KEY]
                     [--since TS] [--limit N] [--no-truncate] [--json]
```

This adds `WorkspaceExport WorkspaceOperation = "export"` to
`internal/model/lifecycle.go`, which forces a decision for all seven
`WorkspaceState` values in the existing exhaustive transition matrix.
Allowed from `WorkspaceCurrent` and `WorkspaceBehind` (a behind Workspace
still exports honestly — it exports what it has, and the sidecar records the
staleness). Refused from `Absent`, `Damaged`, `ArchiveMismatch`,
`InvalidDestination`, `UpgradeRequired`, each with the state's existing
`NextAction` telling the user what to run first.

### Why the exporter may not read the Archive

`docs/redaction-spec.md` is explicit: Archive blobs are **unredacted** (they
are the recovery and provenance path); the corpus index is **redacted**
(it is everything an agent, dashboard, or `aha show` consumer can observe).
An exporter that reached for depot blobs "for fidelity" would publish
unredacted secrets to a file destined to be handed to a third-party agent.

The construction, not the convention:

- `internal/trajectory` **must not import** `internal/depot`,
  `internal/archive`, or `internal/cas`. Enforced by a static test in
  `internal/testquality`, in the same style as the existing network-import
  guard.
- `trajectory.Session` is constructed only by an exported constructor in
  `internal/corpus` that takes `*sql.DB` and reads through the redacted
  read path. `internal/trajectory` exposes no constructor that accepts raw
  bytes, so "build a Session from a blob" is not spellable.

## Data model

### trajectory-v1 → aha (import)

| Record | aha projection |
| --- | --- |
| `meta` | a synthetic entry with `EntryType: "session"`, `RawJSON` = the verbatim meta record; `source`/`cwd`/`git_branch`/`model` also lifted onto `ParsedSession.CWD` and `.Metadata` |
| `user` | `Role: RoleUser`, `Text: content` |
| `reasoning` | `Role: RoleReasoning` (new — see below), `Text: content` |
| `assistant` with `content` | `Role: RoleAssistant`, `Text: content` |
| `assistant` with `tool_calls` | `Role: RoleAssistant`, `ToolCalls: []ParsedToolCall{{ID: id, ToolName: name, FilesJSON: args, Command: <extracted>, Ordinal: i}}`, `Text: ""` |
| `tool` | `Role: RoleToolResult`, `ToolResults: []ParsedToolResult{{ForID: tool_call_id, OutcomeText: content, Ordinal: 0}}`; `IsError` and `ExitCode` left **unset** |

Emitting `meta` as a `type: "session"` entry rather than stashing it in
`ParsedSession.Metadata` is load-bearing: `parseGenericJSONL` already
special-cases `type == "session"`, per-entry `RawJSON` is what the fidelity
test checks, and it is what makes the export side able to reconstruct `meta`
without a new persistence path. Session-level `Metadata` is *not* reliably
round-tripped through the corpus today; do not depend on it.

`Command` extraction from `args`: parse the stringified JSON and take
`cmd`/`command` when present, matching what the existing adapters project.
If `args` is not valid JSON — permitted, the schema only says `"type":
"string"` — keep it verbatim in `FilesJSON`, leave `Command` empty, and
append a diagnostic. Never guess.

**`RoleReasoning` is a new closed-variant member.** `model.Role` is a closed
set consumed by `ShouldIndexRoleText`, whose `default` branch returns
`false`. Mapping reasoning records to an unrecognised role would silently
drop the most interesting content in the corpus from FTS. So:

```go
RoleReasoning Role = "reasoning"
```

indexed like `RoleAssistant` (unconditionally, not gated on
`index_tool_output`). Consequence: the exhaustive `Role × index_tool_output`
check named in `docs/correctness-by-construction-spec.md` grows a row, and
that row must be added in the same commit as the enum member.

**Entry identity.** trajectory-v1 records have no IDs (only `tool_call`s do).
Identity is synthesised as `rec-%06d-<sha256(canonical record JSON)[:12]>`,
mirroring the existing `line-%06d-<sha>` fallback. Same document re-imported
⇒ identical IDs (tested). Known and accepted instability, shared with every
ID-less source we already support: inserting a record shifts the ordinals of
everything after it, so those entries get new IDs. Documented, not hidden.

This adapter is also the forcing function for replacing
`AdapterCapabilities.HasStableEntryIDs` with an identity ladder — see
§Correctness by construction item 8. With one boolean, the trajectory
adapter and Pi are both `false`, which is exactly wrong: Pi's fallback is
anchored to a stable file position, the trajectory adapter's is anchored to
content, and those two support different downstream operations.

### aha → trajectory-v1 (export)

Assistant entries carrying both prose and tool calls — routine in Claude
Code — cannot be represented in one record (property 3). They are **split**,
in order: the prose record first, then the tool-call record, sharing the
source entry's timestamp. Splitting is the only faithful option; dropping
either half would lose data and merging is unrepresentable.

**Untimestamped entries.** Policy, in order:

1. If the entry has a parsable timestamp, normalise it to
   `2006-01-02T15:04:05.000Z07:00` and use it.
2. If the record is a projection of a *timestamped* source entry (a tool
   result split out of one line, or the second half of a split assistant
   entry), inherit that entry's timestamp. This is not invention: it is the
   same source event.
3. Otherwise **drop the record**, increment `Report.Dropped`, and record
   `{entry_id, reason: "no_timestamp"}` in the sidecar.

A drop count above zero is surfaced in the CLI summary and in `--json`. It
is never silent. `--strict` turns any drop into a non-zero exit for callers
that would rather fail than under-export.

**Truncation.** Off for tool-result content below the budget; above it,
content is cut to `--max-tool-result-bytes` (default 4096, tuned per source
— see below) with a `\n…[truncated N bytes]` marker inside the string, since
there is nowhere else to put it. `--no-truncate` disables. The Letta numbers
decompose usefully here and argue against one global default: for Claude
Code, normalisation alone gives 4.5× and truncation adds only to 5.6×; for
Codex, normalisation gives 2.0× and truncation carries it to 5.4×. The
budget is therefore a per-source value on the adapter, overridable by flag,
not a constant.

**The sidecar.** `<out>/<session>.trajectory.json` is accompanied by
`<out>/<session>.aha.json`, carrying everything property 2 forbids in the
document: aha refs per record index, session key, machine ID, source
adapter and version, project key, redaction hit counts, drop list,
truncation accounting, the exporter version, and the vendored schema hash.
The sidecar is aha-owned and versioned as `aha.trajectory.export.v1`.

### The loss table

Stated once, tested continuously (see law L2'), so nobody rediscovers it in
production.

| Field | Export (aha → v1) | Import (v1 → aha) |
| --- | --- | --- |
| Entry/parent IDs, tree structure | lost | synthesised, flat |
| Subagent linkage | lost | absent |
| Token usage, cache split, reasoning tokens, cost | lost | absent |
| `is_error`, exit code | lost | absent (never inferred) |
| Compaction, model-change, thinking-level, label entries | lost | absent |
| Images and assets | lost | absent |
| Session/machine identity | to sidecar | synthesised from path |
| Untruncated tool output | truncated by default | as given |
| `git_branch` | preserved in `meta` | preserved |

## Correctness by construction

Per `agents.md` §2, each of these is a construction, not a runtime check.

**1. The assistant variant is a sum type.** Not a struct with two nullable
fields and a validator:

```go
type AssistantRecord struct{ inner assistantInner } // opaque
func AssistantProse(t Timestamp, content string) (AssistantRecord, error) // rejects empty
func AssistantToolCalls(t Timestamp, calls []ToolCall) (AssistantRecord, error) // rejects empty slice
```

There is no path that produces `{content: "x", tool_calls: [...]}` or
`{content: null}` with no calls, so the schema's `allOf` can never fail on
our output.

**2. Tool-call pairing is held by a builder.** `Document` is assembled
through a builder that owns the set of open call IDs. `AppendToolResult`
takes a `CallRef` that only `AppendAssistantToolCalls` returns, and the
builder refuses to finish while any call ID has no result *or* is
duplicated. An orphan `tool_call_id` is unrepresentable rather than
detected.

**3. `Timestamp` is opaque with a smart constructor.** `ParseTimestamp`
returns a `Timestamp` whose `MarshalJSON` always satisfies the schema
regex. No `string` is ever assigned into a timestamp position.

**4. Export cannot reach unredacted bytes** — the import ban plus the
`corpus`-only constructor, above.

**5. The export destination cannot become an ingest source.** If a user
exports into a directory that is (or is under) a configured source root,
the next `aha archive upload` re-ingests aha's own redacted exports as if
they were agent history — an identity loop that inflates the Archive and
corrupts provenance. `internal/safety.ValidateWriteOutsideSources` already
exists for exactly this shape of problem and is applied to `--out` before
any file is created. The symmetric check applies to a configured
`trajectory` source root that resolves inside the Workspace or Archive
directory: rejected at config validation, before any command mutates state.

**6. Empty documents are unrepresentable.** `minItems: 1`. The builder's
`Finish()` returns a typed error for a session that projected to zero
records; the CLI reports it as a skip with a reason, and never writes a
`[]`.

**7. Export is deterministic.** No map iteration order in output, no
ambient time (`internal/clock`), stable key order. Two exports of the same
session are byte-identical — which is what makes golden tests meaningful
and lets the sidecar carry a content hash.

**8. Entry-identity confidence is a closed variant, not a boolean.**
`AdapterCapabilities.HasStableEntryIDs bool` collapses four materially
different situations into one bit. Replace it with a ladder, adopted from
the vocabulary in `trajectory-canonical-v1`, where each level names the
downstream operation it supports:

```go
type EntryIdentityKind string

const (
	// The manifest predates this field; the kind was not recorded.
	EntryIdentityUnknown   EntryIdentityKind = "unknown"
	// Source-native per-record IDs. Supports dedup and conflict detection.
	EntryIdentityNative    EntryIdentityKind = "native"
	// Stable location anchor (line number, byte offset). Supports
	// append-only assembly; breaks under insertion.
	EntryIdentityLocation  EntryIdentityKind = "location"
	// Content-addressed fallback. Supports exact-duplicate dedup only.
	EntryIdentityContent   EntryIdentityKind = "content"
	// Deterministic identity for synthesised records (e.g. `meta`).
	EntryIdentitySynthetic EntryIdentityKind = "synthetic"
)
```

Why this is scoped into this work rather than left as a wish: the
trajectory importer is the first adapter whose message identity is
content-derived and whose session header is synthetic. Landing it under the
boolean would put it in the same bucket as Pi, whose IDs are native for
wrapper rows and location-anchored otherwise. The distinction is not
cosmetic — `docs/redaction-spec.md` and the conflict-quarantine path both
ask "can these two entries from two machines be the same entry?", and the
honest answer differs per level. It is worth doing on its own merits; the
importer is what makes deferring it cost something.

Three construction details:

- **The boolean does not survive as a second source of truth.**
  `HasStableEntryIDs` becomes a method, `func (c AdapterCapabilities)
  HasStableEntryIDs() bool { return c.EntryIdentity == EntryIdentityNative }`,
  so the two can never disagree. A stored bool alongside a stored kind is
  precisely the runtime-check-instead-of-construction pattern `agents.md`
  rules out.
- **Manifest compatibility is additive, per `docs/compatibility-policy.md`.**
  `AdapterCapabilities` is serialised into `ManifestAdapt`, so this touches
  a written Archive format. `MarshalJSON` emits both the new
  `entry_identity_kind` and the derived `has_stable_entry_ids`, so an older
  binary keeps reading what it always read. That is additive optional
  metadata, which the policy already permits — no new required feature, no
  format major bump.
- **Legacy manifests read back as `unknown`, not as a guess.**
  `UnmarshalJSON` maps a legacy `has_stable_entry_ids: true` to `native`,
  but `false` to `unknown` — because `false` genuinely cannot distinguish
  location from content from synthetic. Inventing precision the bytes do
  not carry would be worse than admitting the gap, and `unknown` being a
  member of the closed set forces every consumer to handle it explicitly
  rather than defaulting into a wrong answer.

## Surfaces

**CLI.** `aha workspace export` as above. Registered in the command registry
in `internal/cli/cli.go` with `Usage`, `Flags`, `FlagSpecs`, `Examples`,
`JSONSchema: "aha.trajectory.export.v1|object{written,skipped,dropped,...}"`,
and `Docs` — all of which the existing `flag_metadata_sync_test.go` and
`docs_test.go` guards require to stay in sync with
`docs/command-inventory.md` and `docs/commands.md`.

**MCP.** One new tool, `export_trajectory`, which returns records **inline**
(bounded by a record and byte cap) and **writes nothing**. The MCP surface
is read-only by construction — `WRITE_TOOLS` is empty and write tools are
intentionally not exposed — and a filesystem-writing MCP tool would be the
first breach. Contract version `aha.mcp.v2` gains the tool additively and it
is discoverable via `aha_capabilities`. The generated TypeScript client
picks it up from the tool registry with no hand-written code.

**Config.** `sources: [{type: "trajectory", root: "...", enabled: true}]`.
`aha init` never adds it; it is always explicit.

## Laws

Each law is a named, testable proposition. The test IDs are referenced from
the test plan below.

- **L1 Schema closure.** For every session `S` in a Workspace,
  `Export(S)` validates against the vendored `trajectory-v1.schema.json`.
- **L2 No invention.** Every string in `Export(S)` is derived from a
  redacted corpus field of `S` — after removing truncation markers, every
  record's content is a prefix of, or equal to, a stored value. Nothing is
  synthesised except the truncation marker itself.
- **L2' Loss-table honesty.** The set of `ParsedEntry` fields that survive
  a round trip equals exactly the set the loss table claims survives. A
  field that starts surviving, or stops, fails the test.
- **L3 Import idempotence.** For a schema-valid document `T`,
  `Export(Import(T)) == Canonicalise(T)`, byte-for-byte, where
  `Canonicalise` normalises only timestamp rendering and key order. This is
  the strong fixed point and it should hold **exactly**, because every one
  of the five record variants has a lossless aha projection.
- **L4 Link integrity.** In any document we emit or accept, every
  `tool.tool_call_id` matches exactly one earlier
  `assistant.tool_calls[].id`; no orphans, no duplicates, no forward
  references.
- **L5 Order preservation.** Record order matches source entry order.
  Timestamps are *not* asserted monotonic — real sources emit out-of-order
  timestamps and asserting otherwise would fail on honest data.
- **L6 Redaction fixed point.** `redact.Apply(Export(S)) == Export(S)`, with
  zero hits. Exported bytes contain no value matching any configured
  pattern, and re-importing an export does not double-mark
  `[REDACTED:...]`.
- **L7 Truncation honesty.** For every truncated record, the `N` in the
  marker equals the exact number of bytes removed, and
  `Report.TruncatedBytes` equals the sum over records.
- **L8 Read-only boundaries.** Import never writes under a trajectory
  source root. Export never writes into a source root, the Workspace
  directory, or the Archive directory.
- **L9 Determinism.** `Export(S)` called twice yields identical bytes.

## Test strategy

This is the part that matters. The format is small enough that near-total
verification is affordable, and the failure modes (publishing a secret,
inventing an error flag, silently dropping half a session) are severe enough
to deserve it.

### Layer map

Mapped onto the layers table in `docs/correctness-by-construction-spec.md`.

| Layer | What it covers here | Where |
| --- | --- | --- |
| Schema contract | L1 — every document validates through **both** validators, and the two agree | `internal/trajectory/schema_test.go` |
| Example/contract | each record variant, each mapping row, each refusal | `internal/adapters/trajectory_test.go`, `internal/trajectory/project_test.go` |
| Conformance fixtures | the existing table-driven adapter harness | `internal/adapters/testdata/conformance/` |
| Golden | byte-stable exported documents and sidecars | `internal/trajectory/testdata/golden/` |
| Property (rapid) | L2, L3, L4, L6, L7, L9 over generated documents | `internal/trajectory/laws_test.go` |
| Fuzz | hostile documents; round-trip fixed point | `FuzzTrajectoryParse`, `FuzzTrajectoryRoundTrip` |
| Exhaustive | role × truncation mode; `Role × index_tool_output`; state × operation | `internal/trajectory`, `internal/model/lifecycle_test.go` |
| Type B | each "unrepresentable" claim in §Correctness attacked directly | `internal/trajectory/typeb_test.go` |
| Differential | our output vs `@letta-ai/trajectory` on the same session | opt-in, not CI |
| CLI journey | init → upload → download → export → re-import → search | `internal/cli/v02_journey_test.go` |
| Static guards | import bans, vocabulary, schema-vendoring integrity | `internal/testquality/` |
| Mutation | `internal/trajectory` added to the critical package list | `scripts/verify.sh mutation` |

### Type A / Type B inventory

Per `agents.md` and the CBC spec: for every invariant, a Type A test that
the postcondition holds for valid inputs, **and** a Type B test that tries to
construct the impossible state and asserts the mechanism rejects it. Type B
tests are written only after the mechanism exists — a Type B test against a
convention documents a fantasy.

| Invariant | Type A | Type B |
| --- | --- | --- |
| Assistant sum type | generated records always satisfy the schema's `allOf` | `AssistantProse("")` and `AssistantToolCalls(nil)` return typed errors; no exported constructor or field assignment can produce both-or-neither — asserted by reflection over the package's exported surface plus a compile-fail `testdata` case |
| Tool-call pairing | generated documents have every call resolved | `AppendToolResult` with a forged `CallRef`, a duplicate result for one call, and `Finish()` with an unresolved call each return typed errors |
| Timestamp | every emitted timestamp matches the schema regex | `ParseTimestamp` rejects `""`, `"2026-13-01T00:00:00Z"`, `"not-a-time"`, a Unix epoch integer, and a date-only string |
| Redaction boundary | export of a secret-bearing corpus yields zero redaction hits | a test that imports `internal/depot` from `internal/trajectory` fails the static guard (asserted by parsing the package's import graph, not by grep) |
| Export/source overlap | export to a fresh directory succeeds | export to a directory inside a configured source root, inside the Workspace, and inside the Archive each refuse **before creating any file** — asserted by checking the directory is still empty after the error |
| Non-empty document | a one-entry session exports | a session projecting to zero records returns a typed error and writes nothing |
| Workspace transition | export allowed from `Current`/`Behind` | export refused from all five other states, each with the state's `NextAction` |
| Validator agreement | both validators accept every valid fixture and every exported document | a document that only `ValidateDocument` rejects (`args` decoding to an array; an unresolved `tool_call_id`) is classified in the committed exception list; an unclassified disagreement fails |
| Identity ladder | each builtin reports its expected `EntryIdentityKind`; `HasStableEntryIDs()` is true exactly for `native` | a legacy manifest with `has_stable_entry_ids: false` reads back as `unknown` and **not** as `content` or `location`; a manifest carrying both fields with contradictory values cannot be constructed, because the boolean is a method with no setter |

### Fixtures and corpora

Three tiers, in the repo's existing pattern:

1. **Hand-written minimal fixtures** — one per record variant, plus the
   awkward cases: `args` that is not valid JSON, `content` containing a
   literal `[REDACTED:...]` marker, a `+05:30` offset timestamp, a
   fractional-second timestamp, non-ASCII and emoji content, a 1 MiB tool
   result, and a document that is exactly one `meta` record (valid per
   `minItems: 1`).
2. **Adversarial fixtures that must be rejected** — orphan
   `tool_call_id`, duplicate call IDs, `content` and `tool_calls` both
   present, `content: null` with no calls, an unknown `role`, an unknown
   property (violating `additionalProperties: false`), an empty array, a
   top-level object instead of an array, invalid UTF-8, a 100 MB single
   record. Each asserts a *typed* error and a diagnostic, never a panic and
   never a partial ingest.
3. **Vendored real sessions** under `testdata/corpora/trajectory-*/`,
   produced by the reference implementation from the Claude Code and Codex
   corpora we already vendor. These feed the existing corpus-wide tests
   automatically.

Fixture discovery helpers (`fixtureBasenames`, `corpusJSONLPaths`) are
JSONL-only today. Extending them to `.json` array documents is part of
Phase 0, and `corpusAdapterFor` gains a `trajectory-*` dirname pattern —
it already `t.Fatalf`s on an unmapped corpus path, so a new corpus
directory cannot land unmapped.

### The conformance gate: two independent validators

The single highest-value test. **No new direct dependency is needed:**
`github.com/google/jsonschema-go v0.4.3` is already in `go.mod` as an
indirect dependency of the MCP SDK and supports draft 2020-12. Phase 0
promotes it to direct.

We validate every document **twice, through two independently written
implementations**, and assert they agree:

- `ValidateSchema(doc)` — the vendored JSON Schema, executed by
  `jsonschema-go`. Declarative, and exactly what an external consumer
  would run.
- `ValidateDocument(doc)` — a hand-written Go validator over our own
  types, checking the same contract imperatively.

This is borrowed directly from the reference implementation, which asserts
both `validateSchema(result.records)` and `validateTranscript(result.records)`
on the same fixtures. The redundancy is the point: a schema can pass
something semantically wrong (`args` that parses as a JSON *array* rather
than an object satisfies `"type": "string"` perfectly), and a hand-written
validator drifts from the schema the moment upstream revises it. Each
catches the other's blind spot, and a disagreement between them is itself
a test failure — the strongest signal available that our understanding of
the contract has diverged from the contract.

`ValidateDocument` additionally carries what the schema structurally
cannot: `tool_call.args` must decode to a JSON **object** (the reference
implementation throws `"tool-call args must encode a JSON object"` here),
and every `tool_call_id` must resolve, which is a cross-record property no
JSON Schema can express.

Four uses:

- **Positive:** every fixture in tier 1 and tier 3, and every document
  produced by `Export` in any test in the suite, passes both validators.
  Implemented as one shared `t.Helper()` that runs both, so *every* export
  test gets the gate for free rather than remembering to ask for it.
- **Negative:** every tier-2 fixture fails. The fixture records **which**
  validator is expected to reject it, so a fixture that only the
  hand-written validator catches proves the schema's limits rather than
  silently passing one of the two.
- **Agreement:** over generated documents (`GenTrajectoryDocument`, plus
  mutations of it), `ValidateSchema(d) == ValidateDocument(d)` except for
  the documented set of properties the schema cannot express. That
  exception set is a committed list, not a loose comment — a new
  disagreement fails until it is classified.
- **Meta:** the vendored schema's own SHA-256 matches
  `PROVENANCE.md`, so an upstream refresh is a visible, reviewed diff.

### Property tests and generators

New shrinkable generators in `internal/testutil`, alongside the existing
parsed-session and manifest generators:

- `GenTrajectoryDocument` — valid documents by construction, with knobs for
  record count, tool-call density, prose length, timestamp offsets and
  precision, and Unicode class.
- `GenAhaSessionForExport` — `ParsedSession`s exercising the awkward export
  paths: assistant entries with prose *and* tool calls, tool results with no
  matching call, entries with no timestamp, entries with only images,
  compaction and model-change entries, an entry whose text is exactly the
  truncation budget, and one byte either side of it.

Laws L2, L3, L4, L6, L7 and L9 are stated as `rapid` properties over these.
L3 is the highest-signal one: it is an exact byte-level fixed point, so it
shrinks to a minimal failing document, and any asymmetry between importer
and exporter surfaces immediately.

L2' (loss-table honesty) is implemented by reflecting over `ParsedEntry`'s
fields, round-tripping a generated session, and comparing the set of fields
that changed against a committed table — the same "classify every field or
fail" discipline as `projection-table.json`. A new `ParsedEntry` field that
nobody classified fails the test.

### Fuzz targets

Added to the `fuzz` and `ci` profiles in `scripts/verify.sh`, alongside the
existing parser/ref/archive/depot targets:

- `FuzzTrajectoryParse` — arbitrary bytes into the importer. No panic;
  malformed input yields diagnostics rather than entries; invalid UTF-8 is
  handled; deeply nested JSON does not blow the stack; a large array does
  not allocate unboundedly (the streaming decoder is the point).
- `FuzzTrajectoryRoundTrip` — structure-aware: mutate a seed corpus of valid
  documents, and for any mutation that still validates against the schema,
  assert L3. This is the target most likely to find real bugs, because it
  explores the importer/exporter asymmetry directly.

Seed corpora are the tier-1 and tier-2 fixtures. Crashers are committed
under `testdata/fuzz/` per the CBC spec's fuzz-corpus management note.

### Differential test against the reference implementation

Opt-in, gated on `AHA_TRAJECTORY_CROSSCHECK=1` and skipped by default, since
it needs Node ≥ 20 and `@letta-ai/trajectory` installed. It takes a vendored
Claude Code session, normalises it with both the reference implementation
and our importer-then-exporter path, and asserts the documents agree
**field-by-field on the intersection**, reporting divergences as a table
rather than a single boolean. Divergences are expected (their adapter reads
the native file; ours reads through the corpus) and each one must be either
fixed or recorded as a known, justified difference. Never in CI — an
external toolchain in the required path would make CI's green light depend
on npm.

### Golden documents

Committed exported documents and sidecars for a small, reviewable set of
sessions, following the golden-update discipline already specified in the
CBC spec: goldens are regenerated only via the documented `UPDATE_GOLDEN=1`
path, and a golden diff in review is a prompt to explain the change, not to
re-run the updater.

**Goldens must not pin a version constant.** The sidecar carries
`model.Version`, the exporter version, and the vendored schema hash — all
of which change on release cadence rather than on behaviour. If goldens
compared them literally, every version bump would rewrite every golden
file, and the resulting noise is precisely what stops a reviewer reading
golden diffs at all. So:

- the golden comparison **excludes** the version fields, via an explicit
  redaction step in the comparison helper — not by omitting them from the
  sidecar, which would weaken the artefact to make the test easy;
- those fields are asserted **separately and by rule**: the exporter
  version equals `model.Version`, and the schema hash equals the one in
  `PROVENANCE.md`. A wrong version still fails, just not in the goldens.

The reference implementation reaches the same conclusion — its canonical
tests state that goldens must not pin the package version while a separate
test asserts `NORMALIZER_VERSION` matches `package.json`. Two assertions,
each about one thing, instead of one assertion about everything.

The same rule applies to any other field that changes on a cadence
unrelated to the behaviour under test. Machine ID and capture timestamp are
already excluded for this reason; version constants join them for the same
reason, and the exclusion list is a named constant in the helper so it can
be reviewed as a whole rather than discovered one `UPDATE_GOLDEN=1` at a
time.

### CLI journey

Extends `v02_journey_test.go` with the full loop, which is the only test
that proves the two halves compose:

```
init → archive upload → archive download → workspace export
     → configure the export dir as a trajectory source in a second config
     → archive upload → archive download → search finds the same needle
```

Asserts along the way: the export directory contains one document and one
sidecar per session; the re-imported session's entry count equals the
exported record count; the needle is findable by `aha search`; and the
second Archive contains no unredacted secret from the seeded fixture. Exit
codes and `--json` contracts are asserted, not just stdout text.

### Static guards

New tests in `internal/testquality`:

- `internal/trajectory` does not import `internal/depot`,
  `internal/archive`, or `internal/cas` — implemented over the parsed
  import graph, so it cannot be defeated by an aliased import.
- the vendored schema hash matches `PROVENANCE.md`.
- the naming rule from §Naming.
- the loss table in this document lists every field the round-trip test
  observed changing — spec and test kept in sync in both directions, in the
  style of the existing spec/lessons ledger test.

### Mutation testing

`internal/trajectory` joins `./internal/model`, `./internal/corpus`,
`./internal/archive`, `./internal/depot`, `./internal/adapters` in
`scripts/verify.sh mutation`. A surviving mutant in **tool-call pairing**,
**the redaction boundary**, **the export-destination check**, or **timestamp
validation** is a release blocker, on the same footing as the existing
ref-parsing and path-safety rules.

### Benchmarks and the token claim

We will want to state a compression figure. It must be one we can
reproduce offline, so:

- benchmark input bytes and output bytes per session, per source, with and
  without truncation, recorded in `docs/performance-results.md` in the
  existing format;
- report an **estimated** token count using a named, deterministic,
  vendored estimator, labelled as an estimate every time it appears;
- **do not** reproduce or compare against Letta's absolute figures. Theirs
  came from a hosted tokeniser over sessions we do not have. Quote their
  *decomposition* (normalisation vs truncation behaving differently per
  source) — that is the transferable finding — and never their ratios as if
  they were ours.

### What we deliberately do not test

Stated so the gaps are chosen rather than accidental:

- **Replay.** Out of scope; no test pretends a trajectory can be re-run.
- **Semantic equivalence of truncated content.** Truncation is lossy by
  design; we test the accounting, not that the meaning survives.
- **Third-party consumers.** We test conformance to the schema, not that
  any particular tool likes our output. The differential test is the
  closest we get, and it is advisory.

## Phased rollout

Each phase is red-green-refactor: the failing tests land first and are
observed failing for the right reason.

**Phase 0 — gates before code.** Vendor the schema + `PROVENANCE.md`;
promote `jsonschema-go` to a direct dependency; write **both** validators
and the shared helper that runs them together, plus the agreement property
and its committed exception list; land tier-1 and tier-2 fixtures, each
recording which validator is expected to reject it; extend the
fixture-discovery helpers to `.json`; add the naming rule to the vocabulary
test. Everything red except the negative fixtures.

**Phase 1 — importer.** `internal/adapters/trajectory.go`; `RoleReasoning`
and its `ShouldIndexRoleText` row plus the extended exhaustive check;
`EntryIdentityKind` replacing `HasStableEntryIDs`, with the derived-method
construction, the additive manifest marshalling, the `unknown` legacy read,
and a declared kind for all five adapters; conformance fixtures; the
fidelity table row; `projection-table.json` classification for every
trajectory key path; `FuzzTrajectoryParse`. Definition of done: a
trajectory document ingests through `aha archive upload`/`download` and is
findable by `aha search`, with no new command.

**Phase 2 — exporter as a library.** `internal/trajectory` with the sum
type, the pairing builder, the opaque timestamp, the corpus-only
constructor; the import-ban static guard; laws L1, L2, L4, L6, L7, L9 as
properties; goldens. No CLI yet — the library is provable on its own.

**Phase 3 — surfaces.** `WorkspaceExport` in the lifecycle enum and its
exhaustive state matrix; `aha workspace export` with registry metadata and
doc sync; the `export_trajectory` MCP tool and regenerated TypeScript
client; `ValidateWriteOutsideSources` on `--out`; the CLI journey test.

**Phase 4 — closing the loop.** L3 and L2' as properties;
`FuzzTrajectoryRoundTrip`; the opt-in differential test; benchmarks and the
`docs/performance-results.md` entry; `internal/trajectory` added to the
mutation list and the survivors triaged.

## Definition of done

- Every law L1–L9 has a passing test, named in the test file after the law.
- Every row of the Type A / Type B inventory has both tests.
- Every document produced anywhere in the suite passes both validators, and
  every disagreement between them is in the committed exception list.
- No golden compares a version constant; the version constants are asserted
  by separate rule-based tests.
- Every adapter in `Builtins()` declares an `EntryIdentityKind`, and no
  stored boolean duplicates it.
- Every trajectory key path observed in any fixture is classified in
  `projection-table.json`.
- The loss table matches what the round-trip test observes, enforced in
  both directions.
- `scripts/verify.sh ci` is green including the two new fuzz targets.
- `make verify-mutation` shows no surviving mutant in pairing, the
  redaction boundary, the destination check, or timestamp validation.
- `docs/commands.md`, `docs/command-inventory.md`, `docs/mcp-spec.md`,
  `docs/compatibility-policy.md`, and `CHANGELOG.md` updated in the same
  commits as the code that changes them.

## Open questions

1. **Should `reasoning` be redacted more aggressively than prose?** Chain
   of thought quotes tool output and pasted material freely. The current
   answer is "same patterns as everything else", which is defensible but
   untested against a reasoning-heavy corpus.
2. **Multi-session documents.** The schema describes one session per
   document. Exporting 500 sessions therefore means 500 files. Is a
   directory the right unit, or do we want a manifest listing them?
   Leaning: a directory plus one `index.json` sidecar, deferred to v1.1.
3. **Do we ever import a trajectory that we exported?** The identity-loop
   guard prevents accidental self-ingest, but a deliberate
   export-on-machine-A, import-on-machine-B flow would create a session
   that exists twice in one Archive under two source identities. Conflict
   quarantine will see them as distinct; that is probably correct but
   should be stated in `docs/trust.md`.
4. **Truncation budget defaults per source.** 4096 bytes is a guess until
   the benchmarks in Phase 4 exist. Set them from measured data, not from
   Letta's numbers.
5. **Does the identity ladder change conflict quarantine?** The ladder
   (§Correctness by construction item 8) gives the quarantine path better
   information than it has today — `native` supports genuine conflict
   detection, `content` supports only exact-duplicate dedup. Whether
   `internal/corpus` should *act* on that, rather than merely record it, is
   not settled here and should not be settled by whoever happens to be
   implementing the importer. Deliberately left open; the ladder is
   additive until someone specifies the behaviour change.
