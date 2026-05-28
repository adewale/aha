# Agent-trace tools: detailed neighbour analysis

Research notes captured 2026-05 on tools adjacent to aha. Use when positioning
aha or borrowing patterns. Eight named tools in depth; ten more catalogued in
the broader survey.

The category is genuinely nascent. Most tools are <1k stars, one-author
projects from late 2025 or early 2026. There is no winner, no shared schema,
and almost no closed-loop write-back (most surface findings without changing
anything).

## 1. Tracebase

`https://github.com/ssreeni1/tracebase` — Node 24+ CLI, npm
`tracebase-local`, **v0.1.0**, **72 stars**, MIT.

**The closest neighbour.** Local-first capture and inspection for Codex +
Claude sessions. Encrypted raw blobs at rest; redacted, searchable metadata
in SQLite + FTS5; localhost dashboard at 127.0.0.1:18427.

### Architecture

Four bin entries: `tracebase`, `traces`, `tcodex`, `tclaude`. Command
groups: setup, capture (import / watch / Claude hooks), inspection (stats,
search, recent, decision-log, trace-diff, run-compare), serve, export,
analyze, MCP.

Three-tier storage at `~/.traces`:

- `index.jsonl` + `sessions.jsonl` — append-only audit logs (durable
  source of truth).
- `blobs/*.json` — AES-256-GCM encrypted raw event payloads.
- `traces.sqlite` (FTS5) — rebuildable projection.

Indexed-safe fields: model, token counts (input/output/cache read/cache
write/reasoning/total), tool name, command/file hints, approval state,
error kind. Everything else stays encrypted.

### Pattern detection — every annotation kind

From `src/analyze.js` ([source](https://github.com/ssreeni1/tracebase/blob/main/src/analyze.js)):

| Kind                          | Detection                                                                                                  | Window                              | Confidence                                                                              |
| ----------------------------- | ---------------------------------------------------------------------------------------------------------- | ----------------------------------- | --------------------------------------------------------------------------------------- |
| `failure`                     | Regex on summary+searchText: `is_error:true`, `exit code [1-9]+`, `command failed`, `no such file`, `permission denied`, `timeout`, `traceback`, `error:`, `failed`, `exception` | per-event                           | 0.95 if `is_error:true` or non-zero exit, else 0.65; severity high for permission/traceback/exception/timeout |
| `resteer`                     | User-role events matching: `try again`, `actually`, `you missed`, `not what i asked`, `wrong`, `fix this`, `rerun`, `new goal`, `redo`, `instead`, `no,`, `that's not`, `you forgot`, `stop` (excludes tool_result events and synthetic prompts) | per-event                           | 0.85 medium                                                                              |
| `recovery`                    | Requires prior `lastFailure`; matches `exit code 0`, `tests passed`, `build succeeded`, `compiled successfully`, `smoke ok`, `done`, `success` | scoped to "after recent failure"    | 0.85 strong / 0.55 weak                                                                  |
| `loop`                        | Same command fingerprint ≥ 3 times                                                                          | **10-minute sliding window**        | 0.75 medium                                                                              |
| `context_waste` (large output)| `outputChars ≥ 12000`                                                                                       | per-event                           | 0.8 medium                                                                               |
| `context_waste` (file reread) | Same file read by read/view/open tool ≥ 3 times                                                             | **20-minute sliding window**        | 0.78 medium                                                                              |
| `context_waste` (search churn)| `rg`/`grep`/`find`/`ag`/`ack` ≥ 5 times                                                                     | **10-minute sliding window**        | 0.7 low                                                                                  |

### Scoring formulas (verbatim, clamped 0–100)

```
qualityScore    = 100 − failure×8  − resteer×12 − loop×10 + recovery×4
efficiencyScore = 100 − repeatedCommand×8 − failedTool×6 − loop×10 − context_waste×7
riskScore       =       redaction×8 + approvalDenied×10 + failure×5
```

Note `riskScore` is **additive from 0** (low risk = 0), whereas
quality/efficiency are 100-minus-penalties. This is almost certainly a
latent inconsistency — borrow the labels, not the formula.

### MCP surface

stdio JSON-RPC 2.0 with `Content-Length:\r\n\r\n` LSP framing. Six
read-only tools, `WRITE_TOOLS = []` empty:
1. `stats` — store metrics
2. `search_events` — FTS over events (required `query`)
3. `list_traces` — canonical traces (optional provider, sessionId)
4. `list_spans` — canonical spans (trace/session/type filters)
5. `session_scorecard` — scorecard + diagnostics (required `sessionId`)
6. `run_compare` — diff two scorecards (required `baseSessionId`, `targetSessionId`)

All schemas use `additionalProperties: false` to reject smuggled args.

### Gaps relative to aha

- **Single-machine only.** No bundle/share/replay; `~/.traces` is the world.
- **No cross-agent unification.** Codex and Claude live in the same store
  but `provider` is just a tag column; events stay provider-scoped.
- **No skill mining / no learned patterns.** Detection is static regex
  tables in one file.
- **No refs / no anchoring.** `eventId` is local; no portable ref.
- **No depot / no team store.**
- **No correctness-by-construction testing.** Fixture-driven smoke/stress/e2e
  only; no property tests, no fuzzing of analyzers.
- **Scoring is hand-tuned magic constants** with no validation.
- **No live agent guidance loop.** MCP is strictly read-only.

### What aha should borrow

- The **redacted-metadata vs encrypted-blob split.** Index the safe fields;
  encrypt the rest.
- **Rebuildable SQLite from append-only logs.** Treat the index as cache,
  not truth.
- **FTS5 on indexed events** is cheap and high-leverage.
- **The 6-tool MCP surface** is a reasonable minimum.
- **`additionalProperties: false`** to harden against arg-smuggling.
- **Run-compare** as a first-class command and dashboard view.
- **The annotation kinds themselves** (failure, resteer, recovery, loop,
  context_waste) as a starting taxonomy.
- **`x-tracebase-raw-export: 1` header gate** for raw-export endpoints —
  prevents accidental leak via hyperlinks or CSRF.
- **Loopback-only bind by default with Origin enforcement** on
  state-changing requests.

## 2. Self-Care

`https://github.com/Not-Diamond/self-care` — Claude Code plugin, **25
stars**, **17 releases**, latest v0.0.17 (2026-04-10), JS 93 %. Three-tier
architecture in `agents/trace-analyzer.md`.

### 14 pathologies (verbatim)

From `agents/skills/`:

`check-ambiguous-instructions`, `check-context-utilization`,
`check-contradictory-instructions`, `check-goal-drift`, `check-grounding`,
`check-guardrail-violation`, `check-instruction-following`,
`check-missed-action`, `check-missing-context`, `check-persona-adherence`,
`check-premature-termination`, `check-reasoning-action-mismatch`, plus
deterministic `tool-failure` and `step-repetition`.

### Architecture

1. **Deterministic Bash tools** for `tool-failure` and `step-repetition`
   (trace-structure analysis, fully reproducible).
2. **Pre-check tools** that emit signals consumed by `missed-action`,
   `premature-termination`, `goal-drift` LLM judges.
3. **Interpretive LLM-as-Judge** skill agents (the other 12) using
   evaluation rubrics, spawned in parallel via Claude's Task tool.

No numeric thresholds are published — the rubric text lives in each
`check-*.md` file. Cases are deduplicated by span; memory persists to
`.self-care/memory/<trace_hash>.jsonl` so cases get `recurring` / `resolved`
status across runs.

### Auto-remediation (the unique part)

`agents/context-refiner.md`. Two phases:

- **Preview** — Glob/Grep for files matching `**/*system*prompt*`, `*.md`,
  `**/tools/**`; compute exact `old_string` / `new_string` diffs; return
  JSON.
- **Apply** — execute the diffs via the Edit tool.

Outcomes logged to `MEMORY.md`. Self-Care *edits your project* — system
prompts, tool-description files, and context Markdown — not the agent
runtime itself.

### Surface

Slash commands inside Claude Code: `/self-care:run`, `/self-care:review`,
`/self-care:autosync-enable`, `/self-care:context`, `/self-care:config`.
Reports land in `.self-care/reports/`.

## 3. claude-session-analyzer

`https://github.com/lucemia/claude-session-analyzer` — Python CLI in a
single `analyze_sessions.py`, **8 stars**, MIT.

**The most numerically grounded of the three analytics tools.** All metrics
have line-cited formulas.

### Metrics with formulas

(Line numbers from `analyze_sessions.py`)

- `Read:Edit ratio = reads / edits` (272–273)
- `Research:Mutation = (reads+greps+globs) / (edits+writes)` (274–275)
- `Write % of mutations = writes / (edits+writes) * 100` (276)
- `Edits without prior Read %` — scans preceding 10 tool calls for matching
  `file_path` Read (300–314); >25 % flagged "degraded" (319)
- `Per-1K-tool-call normalization = count/total * 1000` (368)
- `Daily cost = (in/1e6)*15 + (out/1e6)*75 + (cache_read/1e6)*1.5 + (cache_create/1e6)*18.75` (355–356)
- Thinking-depth proxy = length of `signature` field (Pearson r > 0.95 vs
  content, 797–803)
- Redaction = `thinking_text in ("", None)` (251)

### Pathology phrase lists

Substring `.lower().count()`, not regex:

- Reasoning loops (492–493): `"oh wait"`, `"actually,"`, `"let me reconsider"`,
  `"hmm, actually"`, `"no wait"`, `"hmm actually"`
- Shortcut signal (494): `"simplest"`
- Premature stop / ownership dodge (497–499): `"should i continue"`,
  `"good stopping point"`, `"natural checkpoint"`, `"continue in a new session"`,
  `"known limitation"`, `"not caused by my changes"`, `"existing issue"`
- Self-admitted errors (500–502): `"lazy and wrong"`, `"i rushed"`,
  `"i was being sloppy"`, `"that was wrong"`, `"my mistake"`
- User frustration in prompts (451–454): `"no,"`, `"wrong"`, `"stop"`,
  `"don't"`, `"terrible"`, `"lazy"`, `"fuck"`, `"shit"`, `"broken"`;
  >10 % prompts → "degraded" (700)
- Interruptions: substring `"[request interrupted"`, `"[interrupted"` (445)

Output is a Markdown report `session-analysis-{date}.md`. No external deps.

## 4. agenttrace

`https://github.com/luoyuctl/agenttrace`, [site](https://luoyuctl.github.io/agenttrace/) —
Go 91 %, **51 stars**, **62 releases**, latest v0.5.4 (2026-05-24), MIT. By
far the most actively released of the analytics cluster.

### Pathologies

Verbatim: "long gaps, hanging sessions, retry loops, slow tool calls, large
parameters, and context pressure", plus tool-failure tracking, anomaly
detection, and health regressions vs baselines.

### Heuristics

CLI flags expose the thresholds:
- `--fail-under-health 80`
- `--fail-on-critical`
- `--max-tool-fail-rate 15`

Sessions ranked by latency-stat outliers and anomaly type. Health score
reported as a percentage (avg 91 % in a sample of 1,761 sessions). No
closed-form formula published; sub-signals are tool-failure rate, retry
chains, gap duration, context utilization, and large-param count.

### Surface

Five-pane TUI: Overview (aggregates / spend), List (ranked sessions),
Detail (primary issue + evidence + suggestion), Diagnostics (latency,
context, loops), Diff (run-vs-baseline). Also emits JSON / Markdown / HTML
reports for CI.

Multi-source parsers (15+): Claude Code, Codex CLI, Gemini CLI, Qwen Code,
Cline, Aider, Cursor exports, Hermes Agent, OpenCode, OpenClaw, Pi, Oh My
Pi, Kimi CLI, Copilot-style, generic JSONL.

## 5. skill-optimizer

`https://github.com/hqhq1025/skill-optimizer` — **88 stars**, MIT,
**v2.0.0** (2026-05-14).

Trio of installable Agent Skills: `skill-miner`, `skill-personalizer`,
`skill-generalizer`. Consumes Codex sessions, Claude Code JSONL, Gemini /
Antigravity task files, plus `--export` blobs from other agents. Produces
SKILL.md files with frontmatter plus a sanitized cluster report.

### Algorithm

Deterministic. `scan_sessions.py` walks JSONL, classifies each line by
`type` ("session_meta" / "event_msg" / "response_item" for Codex;
`role`/`type` for Claude), tags messages against regex buckets in
`patterns.json`, then counts intents and tool signals per cluster.
**LLM judgment happens later**, inside the skill itself, reading the
deterministic report as evidence.

### Quotable thresholds

- Intent score: `5 if count >= 12 else 4 if count >= 6 else 3`
- Top workflow score: `count >= 20 and project_spread >= 2`
- Noise filter: `if count < min_count or label == 'other': continue`

The rubric (`references/discovery-rubric.md`) requires both:
- "asked for the same kind of work at least three times", **and**
- a friction signal ("agents consistently miss steps", "non-obvious
  sequencing", "user corrections suggest future agents will make similar
  mistakes").

Explicit warning: "generic coding concepts, one-off facts, and mechanical
checks alone are weak candidates."

### Closes the loop

Strongest of the three skill-mining tools. The miner→personalizer→generalizer
triad produces a publishable artifact and adapts it to local paths/tools.
Still doesn't patch harness config.

## 6. Crune

`https://github.com/chigichan24/crune` — React 19 / TS 5.9 / Vite 8, **9
stars**, Apache-2.0, v0.1.7 (2026-04-27). Claude-only.

Static-web dashboard + npx CLI. Produces `public/data/sessions/{index,overview,detail/*}.json`
for the dashboard *and* synthesized `<name>/SKILL.md` files.

### Algorithm (the richest of the three)

`session-parser.ts` → per-session feature vectors → `knowledge-graph-builder.ts`:

- **TF-IDF** over prompts + responses + file paths + branch names, with
  CamelCase/snake_case splitting.
- **Tool-IDF** down-weighting ubiquitous tools.
- **7-dim structural vector** (user/assistant/tool ratios, subagent
  involvement, edit vs read heaviness).
- **Truncated SVD** at weighted 50/25/25 into 20–80 dims.
- **Agglomerative clustering** with elbow detection on cosine distance.
- **Louvain** community detection.
- **Brandes betweenness** for bridge topics.
- Edges = `0.4*semantic + 0.3*file_overlap + 0.3*session_overlap`, tagged
  `cross-project-bridge | shared-module | workflow-continuation | semantic-similarity`.

### Reusability score

```
0.30*frequency + 0.20*timeCost + 0.20*crossProjectScore +
0.10*recency + 0.10*successRate + 0.10*helpfulness
```

(without facets: 0.35 / 0.25 / 0.25 / 0.15).

Output: D3-force knowledge graph, turn-by-turn session playback with
minimap, overview dashboard, SKILL.md candidates.

Author's own framing: *"the real value is **Skill candidate discovery**"* —
surfacing patterns users "genuinely don't notice."

## 7. retrospective-skill

`https://github.com/accidentalrebel/claude-skill-session-retrospective` —
**8 stars**, MIT, 2 commits.

A single SKILL.md plus a 25-line bash script. Invoked via
`/session-retrospective` or natural triggers ("what did we learn", "lessons
learned", "retro"). Consumes the current session JSONL only.

### Algorithm

Almost none. `scripts/get-session.sh` does
`find "$HOME/.claude/projects" -name "${CLAUDE_SESSION_ID}.jsonl"` and
`cat`s it; Claude itself parses and writes the retro. The SKILL.md
extracts four buckets: Problems & Solutions, Key Decisions, Techniques
Discovered, Mistakes & Corrections.

Output: Markdown to stdout for copy/paste. No file written, no config
touched. Weakest closing-the-loop quality of the three.

## 8. claude-history

`https://github.com/raine/claude-history` — Rust 98.8 %, **296 stars**,
v0.1.62 (2026-05-23, very active).

TUI for fuzzy-searching and reading recent Claude Code conversations from
local JSONL. Commands: bare `claude-history` (browse), `claude-history [FILE]`
(view), `--resume`, `--fork-session`. Flags: `--show-tools`,
`--show-thinking`, `--render`, `--pager`.

No database. Reads JSONL directly. Vim-keyed TUI with in-viewer search,
markdown rendering, mouse-wheel scrolling. Claude Code only.

## Broader neighbour catalogue

Beyond the eight above, the survey turned up:

- **claude-code-transcripts** (simonw) — JSONL → HTML pages with `--gist`
  publish. **1.5k stars.** Python.
- **coding_agent_session_search** (`cass`) — Rust TUI+CLI indexing 20+
  agents (Codex, Claude Code, Cline, Gemini, Cursor, ChatGPT, Aider, etc.).
  SQLite + Tantivy BM25 + optional FSVI vectors. **795 stars.** The most
  technically complete cross-agent indexer.
- **ccfullsearch** (`ccs`, materkey) — Ratatui + ripgrep over Claude Code,
  Claude Desktop, Codex.
- **claude-code-trace** (delexw) — Renders `~/.claude/projects` on desktop,
  web, TUI with live tailing and MCP tool-call detection. **289 stars.**
- **agent-sessions** (jazzyalex) — Swift macOS app with "Agent Cockpit"
  analytics. Covers Codex, Claude, Hermes, Cursor, Gemini, Copilot,
  OpenCode, OpenClaw, Pi. **587 stars.**
- **claude-replay** (es617) — Self-contained embeddable HTML replays for
  Claude/Cursor/Codex/Gemini/OpenCode. **702 stars.**
- **claude-code-log** (daaain) — Python CLI → HTML/Markdown with
  token-usage stats. **1.1k stars.**
- **claude-code-history-viewer** (jhlee0409) — Tauri desktop app covering
  Claude, Gemini CLI, Antigravity, Codex, Cline, Cursor, Aider, OpenCode,
  ForgeCode. **1.4k stars.** Broadest GUI reach.
- Long tail: codex-trace, codex-transcript-viewer, CodexMonitor,
  claude-inspector, cclog, cclv, claude-run, claude-history-manager,
  codex-history-viewer, d-kimuson/claude-code-viewer.

## What the category is missing

Across all 18+ tools:

1. **Cross-agent corpora that act like one corpus.** Only `cass`,
   jazzyalex/agent-sessions, jhlee0409, agenttrace, and aha unify >2
   agents.
2. **Cross-machine.** Only `cass sources setup` and aha's depot/bundle
   model address "my laptop and my desktop both have history."
3. **Semantic search.** Only `cass` ships optional embedders.
4. **Pattern detection** (recurring errors, time-on-task, tool-call
   frequency). Only `cass`'s analytics views and agent-sessions' "Cockpit"
   hint at this.
5. **Longitudinal view.** Nobody answers "what was I working on three
   weeks ago across all my agents."
6. **Shareable refs that survive across machines.** This is the gap aha
   explicitly targets with deterministic `tar.zst` bundles and stable
   refs — no other tool in the survey offers a content-addressed reference
   an agent on a different machine can resolve back to the same turn.

## Cross-cutting taxonomy convergence

A real shared vocabulary is starting to form. Pathology names appearing in
2+ tools:

- **Tool failure / loops / retries** — Self-Care, claude-session-analyzer,
  agenttrace, Tracebase.
- **Premature termination / stopping** — Self-Care, claude-session-analyzer,
  agenttrace.
- **Context utilization / pressure** — Self-Care, agenttrace, partially
  claude-session-analyzer ("edits without prior read").
- **Goal drift** — explicit in Self-Care; absent in agenttrace.
- **Frustration / sycophancy** — only claude-session-analyzer names this
  directly.

If aha defines a portable pathology taxonomy with the converged names
(`tool_loop`, `premature_termination`, `context_waste`, `goal_drift`,
`sycophancy`, `frustration`), it can seed the cross-tool vocabulary the
ecosystem is stumbling toward.

## Closing-the-loop quality (ranked)

1. **Self-Care** — actually edits your CLAUDE.md / system prompts / tool
   descriptions.
2. **skill-optimizer** — produces a publishable SKILL.md adapted to local
   paths.
3. **Crune** — generates SKILL.md candidates but author admits it's
   discovery, not production-ready.
4. **agenttrace** — reports + CI exit codes; never touches prompts.
5. Everything else — pure report, no write-back.

## What a polished aha successor would do

1. Ingest cross-agent traces (already true for aha).
2. Score with explicit pathology detectors using the converged vocabulary
   above, not regex magic constants.
3. Emit not just SKILL.md but `settings.json` patches and hook stubs.
4. **Replay** generated skills against the source JSONL to verify trigger
   precision before installing.
5. Track pathology rate longitudinally so "premature-termination spiked 3x
   after the Sonnet 4.6 → 4.7 swap" becomes a visible regression.
6. Aggregate at the team level over a shared depot, not just for one
   developer on one laptop.

That set is the wedge no other tool occupies.
