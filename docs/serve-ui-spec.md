# `aha serve` UI spec

Status: implemented in `internal/server/ui/` as the search-first dashboard direction.

## Product stance

`aha serve` is a read-only local trace browser for coding-agent history.

The first useful object is not a corpus, task rail, hero headline, or collection of tools. The first useful object is a large search box. A user should be able to type a remembered prompt, file, command, error, or decision and immediately see recognizable slices of real agent work.

Design principle:

> Search first. Results are trace cards, not database rows.

## Research basis

The redesign borrows from existing search and trace products:

- Spotlight, Alfred, and Raycast: large input, keyboard-first recall, compact recognizable result cards with metadata and actions.
- Gmail, Slack, Discord, and Notion: broad search first; snippets preserve the original artifact context; filters refine after intent is expressed.
- Chrome History: recent/history search works because results look like visited artifacts, not records.
- Elastic Discover, Datadog, Jaeger, Zipkin, Sentry: trace/log search results carry status, time, facets, and drilldown, not just text matches.
- GitHub Actions logs: run/job/step hierarchy and persistent status make logs recognizable.
- Cursor, VS Code Copilot/agent mode, and structured tool-use APIs: agent work naturally has trace grammar: prompt, assistant turn, tool call, tool result, files, tests, final response.

## Main jobs

### 1. Recall a thing the user remembers

User question: “Where did I talk about this prompt/file/error/command?”

Flow:

1. User lands on a compact search surface labelled **Search agent history** with a large input for prompts, files, commands, errors, and decisions.
2. User types remembered words.
3. Results render as trace cards grouped by session, not isolated message rows.
4. Each card shows recognizable provenance and matched events.
5. Selecting a card opens **Read selected trace** at the exact ref.

Success criteria:

- The search input is visually dominant and first in source/layout order. Supporting copy must not consume the first screen.
- A first-time user does not need to choose a preliminary task before searching.
- Results look like actual agent-session slices, with server-enriched counts, command chips, file chips, status, and matched events.

### 2. Recognize the right session before opening it

User question: “Is this the one where the agent ran tests and edited the migration?”

Trace-card anatomy:

- title from the most recognizable matched prompt/event;
- project/source/machine/date provenance;
- matched-event count;
- status badge such as conversation, tool work, file match, or failed tool;
- session counts for messages, tool calls, failures, and files;
- command and file chips when available;
- mini event timeline from the actual session shape;
- matched-event snippets with labels: Prompt, Assistant, Tool output, File artifact.

Success criteria:

- Cards are scannable without reading raw JSON or internal IDs.
- Matching text appears in situ with the event type.
- Tool/file/error matches are not flattened into generic snippets.

### 3. Refine after intent is expressed

User question: “Search only prompts” or “search tool output too.”

Flow:

1. User types a query.
2. User can refine via visible **Search in** chips:
   - All history
   - Prompts
   - Assistant replies
   - Tool output
3. Advanced filters remain available but secondary:
   - Project
   - Source
   - Machine
   - Path

Success criteria:

- The UI does not lead with a form full of database facets.
- Refinements use product language; internal `role=user` remains implementation detail.
- Filters are visible in a calm secondary row below the search field.

### 4. Read the selected trace

User question: “Show me what happened around this match.”

Flow:

1. User selects a trace card.
2. The app calls `read` for the selected canonical ref.
3. **Read selected trace** shows structured transcript entries, not one raw preformatted blob.
4. URL hash stores the selected ref for reloadable context.

Success criteria:

- Search results remain leads; the reader provides evidence.
- The reader is framed as selected trace context, not a generic “read” command.

### 5. Investigate recurring failures

User question: “What keeps breaking, and has this been fixed before?”

Flow:

1. User searches normally, or scrolls to **Recurring failures**.
2. Failure state controls use human labels:
   - all
   - needs attention
   - sometimes fixed
   - fixed before
3. Each failure row can search matching history, open an example, trace a fix path, or copy fix notes.

Success criteria:

- Failure analysis is still available but no longer competes with search as the first screen.
- The phrase **copy fix notes** remains literal: the dashboard copies text only; it does not write or install skills.

### 6. Trust the archive

User question: “What data is indexed, and is anything quarantined?”

Flow:

1. User reads **Archive health** below the primary search/results surface.
2. Counts and chips show indexed sources, machines, projects, and span.
3. Clicking a chip scopes the next search and recurring-failure filters.
4. A visible scope summary appears with **Clear scope**.
5. **Merge conflicts** lists quarantined rows.

Success criteria:

- Health is useful but not the mental model of the app.
- Chips are navigation controls, not decorative metrics.
- Quarantined data is clearly separated from trusted search/read paths.

## Information architecture

```text
┌────────────────────────────────────────────────────────────────────────────┐
│ aha · local agent memory                    sessions · entries · messages │
├────────────────────────────────────────────────────────────────────────────┤
│ Search agent history                                                       │
│ ┌────────────────────────────────────────────────────────────────────────┐ │
│ │ schema migration sqlite failure                                        │ │
│ └────────────────────────────────────────────────────────────────────────┘ │
│ Search in: [All history] [Prompts] [Assistant replies] [Tool output]       │
│ Advanced filters: project/source/machine/path                              │
│                                                                            │
│ Trace cards                                                                │
│ ┌────────────────────────────────────────────────────────────────────────┐ │
│ │ old v14 failure_episodes CHECK constraints                             │ │
│ │ aha · claude-code · machine · 2026-06-04 · 3 matched events             │ │
│ │ [failure match]                                                         │ │
│ │ timeline: ● prompt ─ ● assistant ─ ● tool                               │ │
│ │ Prompt       old v14 failure_episodes CHECK constraints...              │ │
│ │ Tool output  go test ./internal/corpus ... failed                       │ │
│ └────────────────────────────────────────────────────────────────────────┘ │
├────────────────────────────────────────────────────────────────────────────┤
│ Read selected trace                                                        │
│ original transcript context around selected ref                            │
├────────────────────────────────────────────────────────────────────────────┤
│ Recurring failures                                                         │
│ [all] [needs attention] [sometimes fixed] [fixed before] + facets          │
├──────────────────────────────────────────────┬─────────────────────────────┤
│ Archive health                               │ Merge conflicts             │
└──────────────────────────────────────────────┴─────────────────────────────┘
```

## API mapping

| UI concept | API/tool backing | Notes |
|---|---|---|
| Search field | `POST /api/search_traces` | Uses `query` plus optional role/project/source/machine/path, then enriches grouped hits into trace cards. |
| Search in: All history | omit `role` | Search messages and artifacts. |
| Search in: Prompts | `role = "user"` | UI label stays product-facing. |
| Search in: Assistant replies | `role = "assistant"` | Searches assistant-authored messages. |
| Search in: Tool output | `role = "toolResult"` | Searches indexed tool-result messages. |
| Trace cards | grouped enriched search hits | Server groups hits by `session_key`, adds counts, timeline, command chips, file chips, status, and matched events. |
| Read selected trace | `POST /api/read` | Uses the clicked card's `ref_text`. |
| Recurring failures | `POST /api/incidents` | State labels map to corpus states. |
| Trace fix | `POST /api/incident_trajectory` | Requires sample ref and ordinal. |
| Archive health | `GET /api/overview` | Counts and scope chips. |
| Merge conflicts | `GET /api/conflicts` | Quarantined rows. |

## Interaction rules

- Empty state is not a task rail. It tells the user to search and explains trace-card output.
- The search box searches all history by default.
- Search chips set the hidden role filter, update `aria-pressed`, and rerun the search when a query is present.
- Advanced filters are secondary and collapsed by default.
- Scope changes show a visible summary and a live feedback sentence so users know what changed.
- Search results come back as enriched trace cards grouped by session key.
- Selecting any trace card loads the first matched ref in **Read selected trace** and highlights the selected entry when possible.
- Overview chips populate search and incident facets, then focus the search box.
- Incident rows and fix paths continue to drill into read context.
- Clipboard actions are user-initiated only.

## Copy vocabulary

Prefer:

- Search agent history
- trace cards
- Search in
- All history
- Prompts
- Assistant replies
- Tool output
- Advanced filters
- Read selected trace
- Recurring failures
- needs attention
- sometimes fixed
- fixed before
- copy fix notes
- Archive health
- Merge conflicts

Avoid as primary UI:

- Start with a task
- journey
- corpus
- read
- incidents
- conflicts
- cluster
- skill draft

Technical terms may remain in code/API docs, not in the main user-facing dashboard language.

## Quality bar

- No task rail that points at other UI. The interface itself should make the task obvious.
- No oversized explanatory hero copy above search. Put explanation in empty states and feedback.
- No side-tab card accents, gradient text, glass effects, nested card stacks, or generic hero metrics.
- Use tinted OKLCH neutrals and restrained semantic color. Text colors must not use pure black or pure white.
- Every clickable control has visible effect: active state, live feedback, changed results, changed scope, or opened context.
- Prefer spacing, grouping, and labels over extra instructional cards.

## Layout requirements

Desktop:

- The compact search surface spans the full width and appears first.
- Search input is large, but its label and hint are compact enough that results stay close to the fold.
- Trace cards and reader sit in one explicit workbench grid so their edges align.
- The reader is not sticky; sticky layering can overlap later sections and make scroll state ambiguous.
- Recurring failures are below the workbench.
- Archive health and merge conflicts sit in one lower-priority panel row.

Mobile/narrow:

- Single-column order: search, trace cards, reader, recurring failures, archive health, merge conflicts.
- Search button stacks below input.
- Advanced filters stack to one column.
- Trace cards keep event labels and snippets readable.

## Accessibility requirements

- The search label is a real `<label for="query">` and visible.
- Search chips are real buttons with `aria-pressed`.
- Trace cards are real buttons, not clickable anonymous rows.
- Inputs have visible labels where filters are shown.
- State buttons expose active state visually and with `aria-pressed`.
- Trace timelines are decorative summaries; event snippets carry the textual content.

## Privacy and trust boundaries

- The UI is read-only.
- It uses only same-origin local endpoints served by `aha serve`.
- It does not fetch remote assets.
- It does not snapshot, ingest, repair, edit source histories, write skills, or install anything.
- Search/read output remains governed by the corpus projection and existing redaction behavior.
- Bundles/depots remain raw provenance outside this dashboard surface.

## Regression coverage

`internal/server/server_test.go` includes `TestDashboardIsSearchFirstTraceBrowser`, which locks in:

- the compact search-first label;
- trace-card/result language;
- search chips and advanced filters;
- removal of task-rail language;
- replacement of stale/internal copy such as `cluster` and `copy skill draft`.

## Non-goals

- Building a full observability waterfall renderer.
- Synthesizing session titles server-side.
- Running commands from the browser.
- Replaying/resuming sessions.
- Writing or installing skills.
- Mutating source histories or corpus rows.
