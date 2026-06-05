# Outcome-weighting spec

Status: design (not yet implemented). Builds directly on the shipped
error-cluster surface (`internal/corpus/clusters.go`, migration 13
`tool_invocations`). This document specifies the *second half* of the
"sessions → skills" idea: turning a recurring **failure** cluster into a
recommended **fix**, scored by whether that fix actually worked.

## Why

`aha clusters` answers *"which tool-call failures recur and spread?"* It ranks
recurring failures by `count × spread` (`clusters.go:336`) and hands back a
`sample_ref` for drill-in. That is genuinely useful — it surfaces skill
*topics* — but it stops one step short of the thing a skill is *for*: the user
or agent still has to go read the transcripts and work out the fix by hand.

The differentiated claim of the whole "tapes → skills" direction was never
"summarize a debugging session." It was **extract the fix that worked, not the
fix that was merely common.** Those differ constantly: the modal *first* action
after an auth error is often the modal *wrong turn* (retry the same command),
and only a later step resolves it. Ranking by raw frequency codifies folklore;
ranking by outcome codifies fixes.

Two ideas compose here, and each alone is misleading:

- **Recurrence-clustering** (already shipped) gives the right *denominator*: a
  pattern seen N times across sessions/projects, not a one-off story.
- **Outcome-weighting** (this spec) gives the right *numerator*: of the paths
  taken out of that failure, which ones reached a verified success, how often,
  and how reliably.

Recurrence without outcome ⇒ "everyone does it this way, must be right" (ships
popular wrong turns). Outcome without recurrence ⇒ "this one session ended ok,
here's the skill" (mistakes an idiosyncratic local fix for a general
technique). Together: *here is a pattern that recurred N times and resolved
successfully ≥M times; the skill is the resolution path.*

## The foundation already exists

This spec adds an analysis layer over data that migration 13 already captures.
No new capture, no re-ingest, no new redaction boundary.

`tool_invocations` (`tool_invocations_migration.go:11`) stores **one row per
paired tool call with an observed outcome** — and crucially it stores
**successes as well as failures**. The backfill inserts every invocation where
`OutcomeObserved` is true (`tool_invocations_migration.go:140`), stamping
`is_error`, `exit_code`/`exit_code` validity, `error_signature`,
`command_family`, `timestamp`, `session_key`, `project_key`, and `entry_id`.
`clusters.go` only ever queries `where is_error=1`, so the success rows are
present but currently unread. **Outcome-weighting is the consumer of those
success rows.**

That means the entire layer is, in principle, a deterministic query plus a new
append-only projection table — the expensive parts (cross-adapter `tool_use`↔
`tool_result` pairing, privacy-preserving normalization, append-only storage)
are done.

### What we deliberately do *not* have, and will not invent in v1

- **No cross-session episodes.** `tool_invocations` has no
  `previous_session_id`; `ParentID` links only within a session. A debugging
  episode that spans two sessions (stopped, resumed tomorrow) is **out of scope
  for v1**. Episodes are scoped to a single `session_key`. This is a documented
  accepted limit, not a bug.
- **No linguistic reward.** "Agent said 'that worked'" is not in
  `tool_invocations` and we will not reach back into raw transcript text to get
  it — doing so would reopen the `index_tool_output=false` bypass that
  `clusters.go` was careful to avoid (`clusters.go:23`). v1 reward is **hard
  signals only**: `is_error` and `exit_code`. This is a correctness *and* a
  privacy decision.
- **No raw outcome text in the new surface.** Same fail-closed rule as the
  cluster backfill (`tool_invocations_migration.go:148`): the outcome-weighting
  surface displays only already-normalized `command_family` and
  `error_signature`, never raw stdout/stderr.

## Definitions

**Invocation.** One row of `tool_invocations`: a tool call with an observed
outcome (`is_error ∈ {0,1}`, optional valid `exit_code`).

**Failure episode.** Within a single `session_key`, order invocations by
`(timestamp, entry line order, ordinal)` — the same deterministic order the
backfill uses. A failure episode for command family `F` with signature `S`:

- **opens** at a failing invocation (`is_error=1`) whose
  `(command_family, error_signature) = (F, S)`;
- **closes — resolved** at the next *later* invocation in the same session with
  `command_family = F` and `is_error=0` (the thing that was failing now
  succeeds);
- **closes — abandoned** if no such success exists before the session ends, or
  before a bounded gap (`max_episode_gap`, default 30 min) or step budget
  (`max_episode_steps`, default 40 invocations) is exceeded.

Requiring the resolving success to share the **same command family** as the
opening failure is the key guard against "the agent gave up and an unrelated
later command happened to succeed." We only call it a resolution when *the thing
that was broken* starts working.

**Resolution path.** For a resolved episode, the ordered sequence of
`command_family` values of the invocations strictly between the opening failure
and the resolving success, followed by the resolving family itself. This is the
candidate fix body — "after `F` failed with `S`, the agent ran A, B, then `F`
succeeded." Paths are compared by their normalized family sequence, so the same
fix across repos/ids collapses (the normalization already lives in
`commandFamily`/`normalizeErrorSignature`).

**Skill candidate.** A failure cluster `(tool_name, command_family,
error_signature)` that has ≥1 resolved episode. Its recommended fix is the
highest-confidence resolution path (below).

## Outcome-weighting

For each cluster `C`:

1. Enumerate its failure episodes across all sessions/projects.
2. Partition into `resolved` and `abandoned`. Record
   `resolution_rate = resolved / (resolved + abandoned)` — a property of the
   *cluster* ("when this fails, does it usually get fixed at all?").
3. Among resolved episodes, group by resolution path. For each distinct path
   `P`: `support_P` = number of resolved episodes taking `P`; `sessions_P`,
   `projects_P` = distinct spread.
4. Rank paths by a **small-N-aware** score, not raw success count:

   ```
   confidence_P = wilsonLowerBound(successes = support_P,
                                   trials   = support_P + competing_failures_at_same_step,
                                   z        = 1.96)
   pathScore_P  = confidence_P × spread(sessions_P, projects_P)
   ```

   The Wilson lower bound is the correctness-by-construction guard against the
   `1/1 = 100%` overconfidence trap: a path seen once scores well below a path
   seen ten times even though both are "100% successful." `spread()` reuses the
   exact cluster spread weighting (`clusters.go:340`) so a path that worked
   across many projects outranks one that worked repeatedly in a single repo.

5. Emit the **top-K distinct paths** (default K=3), not a single winner.
   Clusters are frequently *confounded* — one `error_signature` with several
   root causes ("permission denied" for three different reasons) — and forcing a
   single recommended fix would be confidently wrong on the other causes.
   Multi-modal output is the honest representation; the caller (human or agent)
   picks the path matching their context.

### Single-user honesty

At single-user scale most clusters have small N — often a single resolved
episode. The design degrades *gracefully* rather than lying:

- A path with `support=1` is surfaced, but its low Wilson bound and a
  `confidence: "low"` band make the thin evidence explicit.
- Two presentation tiers mirror the personal-vs-promote distinction:
  `tentative` (resolved ≥1) and `established` (resolved ≥3 across ≥2 sessions).
- The output never asserts "the fix is X." It asserts "in N observed episodes,
  path X resolved this M times" — evidence, not folklore.

## Public surface

### Corpus API

```go
// internal/corpus/outcomes.go
type ResolutionPath struct {
    Families   []string `json:"families"`     // normalized command-family sequence, fix tail last
    Support    int      `json:"support"`      // resolved episodes taking this path
    Sessions   int      `json:"distinct_sessions"`
    Projects   int      `json:"distinct_projects"`
    Confidence float64  `json:"confidence"`   // Wilson lower bound
    SampleRef  string   `json:"sample_ref,omitempty"` // msg ref to the resolving success
}

type SkillCandidate struct {
    ToolName       string           `json:"tool_name"`
    CommandFamily  string           `json:"command_family"`
    ErrorSignature string           `json:"error_signature"`
    Episodes       int              `json:"episodes"`        // resolved + abandoned
    Resolved       int              `json:"resolved"`
    ResolutionRate float64          `json:"resolution_rate"`
    Paths          []ResolutionPath `json:"paths"`           // top-K, ranked
    Score          float64          `json:"score"`
    Tier           string           `json:"tier"`            // "tentative" | "established"
}

// SkillCandidates ranks resolved failure clusters with their best fixes.
// limit<=0 uses the default page size; positive limits clamp to MaxClusterLimit.
func SkillCandidates(db *sql.DB, limit int) ([]SkillCandidate, error)
```

`SkillCandidate` is a strict superset of `Cluster` (same first three identity
fields, same `Score`/spread machinery), so it composes with the existing
`clusters` plumbing rather than forking it.

### CLI

Extend the existing read-only command rather than add a new verb:

```
aha clusters [--repo DIR] [--limit N] [--with-fixes] [--json]
```

- Without `--with-fixes`: today's behaviour, unchanged (pure failure ranking).
- With `--with-fixes`: rows carry the top resolution path(s), resolution rate,
  tier, and a `sample_ref` to the *resolving success* row (so `aha read`
  drills into the command that fixed it, not just the one that failed).

`clusters` stays in the read-only command set (`README.md:280`). No new mutating
surface.

### MCP / HTTP / TS (follow-up, not v1)

The `clusters` tool already crosses MCP/HTTP/UI/TS. Adding a `with_fixes`
boolean to that one tool extends all four surfaces uniformly, following the
existing codegen/conformance pattern. Scoped as a fast-follow once the corpus
function and CLI are green, to keep the first change reviewable.

## Storage

A new append-only projection table, deterministically rebuildable from
`tool_invocations` (which is itself rebuildable from `entries.raw_json`).
Following the established pattern: `if not exists` schema, no-update/no-delete
triggers, foreign keys, a versioned migration (**migration 14**).

```sql
create table if not exists failure_episodes(
  session_key      text,
  open_entry_id    text,
  open_ordinal     integer,
  tool_name        text,
  command_family   text,
  error_signature  text,
  resolved         integer not null check(resolved in (0,1)),
  resolve_entry_id text,                 -- null when abandoned
  resolution_path  text,                 -- json array of command families, null when abandoned
  project_key      text,
  opened_at        text,
  resolved_at      text,                 -- null when abandoned
  primary key(session_key, open_entry_id, open_ordinal),
  foreign key(session_key, open_entry_id) references entries(session_key, entry_id)
);
create index if not exists idx_failure_episodes_cluster
  on failure_episodes(tool_name, command_family, error_signature, resolved);
```

`SkillCandidates` aggregates over `failure_episodes`; the path ranking is a
query over the JSON `resolution_path` column. Episodes are recomputed for a
session whenever its invocations change (same hook point as the
`tool_invocations` backfill), so the table is a pure function of the corpus and
carries no independent truth.

**Correctness-by-construction notes:**

- An episode row cannot exist without a failing opener (the writer only emits
  rows for `is_error=1` openers; enforced by a smart constructor in Go and by
  the `resolved`/`resolve_entry_id` shape — `resolved=1 ⟺ resolve_entry_id is
  not null`, checked).
- `resolution_path` is only non-null when `resolved=1`; a CHECK ties them so an
  abandoned episode cannot carry a phantom fix.
- Determinism: identical corpus ⇒ identical `failure_episodes` ⇒ identical
  `SkillCandidates` output, including tie-breaks (reuse the lexical ordering
  tail from `Clusters`, `clusters.go:275`).
- No redaction bypass: every stored/displayed string is an already-normalized
  `command_family` or `error_signature`; `resolution_path` is a list of
  families, never raw commands or outputs.

## Implementation plan (red-green-refactor, per agents.md)

Each step starts with a failing test that pins the behaviour, then the minimal
code to pass, then refactor. No production code without a failing test first.

1. **Episode assembly — `assembleEpisodes([]ToolInvocation) []FailureEpisode`.**
   - Red: table-driven tests over hand-built invocation slices — resolved,
     abandoned-by-end, abandoned-by-gap, abandoned-by-budget, interleaved
     families (two unrelated failures concurrently must not cross-contaminate),
     same-family-success-as-resolution, unrelated-success-is-not-resolution.
   - Green: the windowing function. Pure, no DB — directly unit-testable.
   - Property test: every emitted episode opens on `is_error=1`; `resolved=1 ⟺
     resolve_entry_id set`; resolution path families are a contiguous in-session
     subsequence ending in the resolving family.

2. **Wilson scoring — `wilsonLowerBound`, `pathScore`.**
   - Red: known fixtures (`1/1 < 10/10`; `5/5` vs `50/55`; monotonicity).
   - Green: the closed-form Wilson bound.
   - Property: score is monotone non-decreasing in support at fixed rate; always
     in `[0,1]` for the bound.

3. **Migration 14 + backfill.**
   - Red: schema-introspection + state-machine tests (append-only triggers
     reject update/delete; CHECK rejects `resolved=0` with a non-null path;
     backfill from a seeded `tool_invocations` yields expected episodes).
   - Green: migration entry, `if not exists` DDL, triggers, backfill loop
     mirroring `backfillToolInvocations`.

4. **`SkillCandidates` aggregation.**
   - Red: end-to-end over a seeded corpus — the worked Confluent example below
     must produce the expected ranked paths, rates, and tiers.
   - Green: the aggregating query + top-K path selection.

5. **CLI `--with-fixes`.**
   - Red: `cli_test.go` cases for human and `--json` output, including the
     empty-corpus and no-resolved-episodes paths.
   - Green: flag wiring in `command_clusters.go`.
   - Refactor: docs (`docs/commands.md`), `README.md` cluster bullets,
     `CHANGELOG.md`, and the architecture HTML drift test if the surface list
     changed.

## Worked example (the canonical Confluent case)

Six failure episodes of `(Bash, "confluent kafka topic", "unknown topic or
partition")` across two repos:

| Episode | Path between failure and same-family success | Outcome |
|---|---|---|
| 1 | (none — retried same command) | abandoned |
| 2 | set `sasl.mechanism`, set `sasl.jaas.config` → success | resolved |
| 3 | set `sasl.mechanism`, set `sasl.jaas.config`, set `security.protocol` → success | resolved |
| 4 | set `sasl.mechanism`, set `sasl.jaas.config` → success | resolved |
| 5 | switched to local Kafka (different family, never same-family success) | abandoned |
| 6 | set `sasl.mechanism`, set `sasl.jaas.config` → success | resolved |

Outcome-weighted result:

- `resolution_rate = 4/6` for the cluster.
- Path **`[set sasl.mechanism, set sasl.jaas.config]`**: support 3, two sessions
  ⇒ highest confidence, `tier: established`.
- Path **`[set sasl.mechanism, set sasl.jaas.config, set security.protocol]`**:
  support 1, `tier: tentative` — surfaced as a context-dependent variant, not
  buried.
- The retry-only and switch-to-local episodes are correctly *not* fixes.

A single-session extractor sampling episode 1 or 5 would have emitted a
non-fix; sampling episode 3 would have over-specified `security.protocol` as
mandatory. Outcome-weighting recovers "both fields always; `security.protocol`
sometimes" — evidenced by the distribution, not asserted from one transcript.

## Accepted v1 limits

- Episodes are single-session; cross-session debugging arcs are not linked.
- Reward is hard-signal only (`is_error`, `exit_code`); no linguistic
  resolution detection.
- "Same command family succeeds" is the resolution criterion; a genuine fix that
  manifests as a *different* family succeeding (rare) is scored as abandonment.
  Conservative on purpose — better to under-claim a fix than to ship a wrong one.
- Confounded clusters are handled by top-K multi-path output, not by splitting
  the cluster; deeper root-cause separation (adding context axes to the
  signature) is deferred.
