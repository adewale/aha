# `aha dashboard` UI spec

Status: implemented in `internal/server/ui/` as the search-first dashboard direction.

## Product stance

`aha dashboard` is a read-only local trace browser for coding-agent history.

The first useful object is not a corpus, task rail, hero headline, or collection of tools. The first useful object is a large search box. A user should be able to type a remembered prompt, file, command, error, or decision and immediately see recognisable slices of real agent work.

Design principle:

> Three journeys, one domain model. Search finds a trace, Failures mines patterns across traces, Sources explains indexed data, scope, and trust.

Visual direction: restrained technical ledger. The UI should feel like a precise local instrument: dense, aligned, rule-based, and quiet. Avoid decorative spectacle; make the memorable move the clarity of the evidence trail.

Execution rules:

- Navigation uses labels only; avoid ordinal badges that look like ranking or indexing.
- Trace selection is mirrored in Evidence with an explicit selected-ref header and `aria-current` state.
- Result and evidence cards must never paint outside their own bounds or overlay neighboring cards.
- Typography is label/mono-forward for provenance and controls, with restrained body text for transcript content.
- Motion is limited to quick state transitions and disabled under reduced-motion preferences.

Top-level tabs:

- **Search**: find context inside traces, with prompts selected first.
- **Failures**: inspect the most frequent recurring failure patterns and fixes.
- **Sources**: understand indexed sources, scope, and trust issues. It stays out of the main workspace unless the user needs data confidence or scope.

## Research basis

The redesign borrows from existing search and trace products:

- Spotlight, Alfred, and Raycast: large input, keyboard-first recall, compact recognisable result cards with metadata and actions.
- Gmail, Slack, Discord, and Notion: broad search first; snippets preserve the original artefact context; filters refine after intent is expressed.
- Chrome History: recent/history search works because results look like visited artefacts, not records.
- Elastic Discover, Datadog, Jaeger, Zipkin, Sentry: trace/log search results carry status, time, facets, and drilldown, not just text matches.
- GitHub Actions logs: run/job/step hierarchy and persistent status make logs recognisable.
- Cursor, VS Code Copilot/agent mode, and structured tool-use APIs: agent work naturally has trace grammar: prompt, assistant turn, tool call, tool result, files, tests, final response.

## Main jobs

### 1. Recall a thing the user remembers

User question: “Where did I talk about this prompt/file/error/command?”

Flow:

1. User lands on a compact search surface labelled **Search agent history** with a large input for prompts, files, commands, errors, and decisions.
2. User types remembered words.
3. Results render as trace cards grouped by session, not isolated message rows.
4. Each card shows recognisable provenance and matched events.
5. Selecting a card opens **Evidence** at the exact ref.

Success criteria:

- The search input is visually dominant and first in source/layout order. Supporting copy must not consume the first screen.
- A first-time user does not need to choose a preliminary task before searching.
- Results look like actual agent-session slices, with server-enriched counts, command chips, file chips, status, and matched events.

### 2. Recognise the right session before opening it

User question: “Is this the one where the agent ran tests and edited the migration?”

Trace-card anatomy:

- title from the first user prompt when available, with matched events shown separately;
- project/source/machine/date provenance;
- matched-event count;
- status badge such as conversation, tool work, file match, or failed tool;
- session counts for messages, tool calls, failures, and files;
- command and file chips when available;
- mini event timeline from the actual session shape;
- matched-event snippets with labels: Prompt, Assistant, Tool output, File artefact.

Success criteria:

- Cards are scannable without reading raw JSON or internal IDs.
- Matching text appears in situ with the event type.
- Tool/file/error matches are not flattened into generic snippets.

### 3. Refine after intent is expressed

User question: “Search only prompts” or “search tool output too.”

Flow:

1. User types a query.
2. Prompt search is selected first.
3. User can refine via visible **Search in** chips:
   - Prompts
   - All history
   - Assistant replies
   - Tool output
4. Advanced filters remain available but secondary:
   - Project
   - Source
   - Machine
   - Path

Success criteria:

- The UI does not lead with a form full of database facets.
- Refinements use product language; internal `role=user` remains implementation detail.
- Filters are visible in a calm secondary row below the search field.

### 4. Read the selected evidence

User question: “Show me what happened around this match.”

Flow:

1. User selects a trace card.
2. The app calls `read` for the selected canonical ref.
3. **Evidence** shows structured transcript entries, not one raw preformatted blob.
4. Evidence exposes actions to copy the canonical ref and widen transcript context.
5. URL hash stores the selected ref for reloadable context.

Success criteria:

- Search results remain leads; the reader provides evidence.
- The reader is framed as evidence for the selected trace event, not a generic “read” command.

### 5. Investigate recurring failures

User question: “What keeps breaking, and has this been fixed before?”

Flow:

1. User opens the **Failures** tab when they want summaries rather than one trace.
2. The tab starts with the **most frequent** failure pattern so the user has a concrete next action.
3. Failure state controls use human labels:
   - all
   - needs attention
   - sometimes fixed
   - fixed before
4. Each failure row can search matching history, open an example, trace a fix path, or copy fix notes.

Success criteria:

- Failure analysis is still available but no longer competes with search as the first screen.
- The phrase **copy fix notes** remains literal: the dashboard copies text only; it does not write or install skills.

### 6. Trust the archive

User question: “What data is indexed, and is anything quarantined?”

Flow:

1. User opens the **Sources** tab.
2. User reads **Sources & scope** for indexed sessions, messages, sources, machines, projects, and span.
3. Clicking a chip scopes the next search and recurring-failure filters.
4. A visible scope summary appears with **Clear scope** in Search.
5. **Trust checks** lists quarantined rows when there is a problem.

Success criteria:

- Health is useful but not the mental model of the app.
- Chips are navigation controls, not decorative metrics.
- Quarantined data is clearly separated from trusted search/read paths.

## Information architecture

```text
┌────────────────────────────────────────────────────────────────────────────┐
│ aha · local agent memory                    sessions · entries · messages │
├────────────────────────────────────────────────────────────────────────────┤
│ [ Search ] [ Failures ] [ Sources ]                                        │
│                                                                            │
│ Search tab                                                                 │
│ Search agent history                                                       │
│ ┌────────────────────────────────────────────────────────────────────────┐ │
│ │ schema migration sqlite failure                                        │ │
│ └────────────────────────────────────────────────────────────────────────┘ │
│ Search in: [Prompts] [All history] [Assistant replies] [Tool output]       │
│ Advanced filters: project/source/machine/path                              │
│                                                                            │
│ Trace / Conversation → Event → Evidence                                    │
│                                                                            │
│ Traces                                                                     │
│ ┌────────────────────────────────────────────────────────────────────────┐ │
│ │ old v14 failure_episodes CHECK constraints                             │ │
│ │ aha · claude-code · machine · 2026-06-04 · 3 matched events             │ │
│ │ [failure match]                                                         │ │
│ │ timeline: ● prompt ─ ● assistant ─ ● tool                               │ │
│ │ Prompt       old v14 failure_episodes CHECK constraints...              │ │
│ │ Tool output  go test ./internal/corpus ... failed                       │ │
│ └────────────────────────────────────────────────────────────────────────┘ │
├────────────────────────────────────────────────────────────────────────────┤
│ Evidence                                                                   │
│ original transcript context around selected ref                            │
├────────────────────────────────────────────────────────────────────────────┤
│ Failures tab                                                               │
│ recurring failure patterns, fix paths, evidence links                      │
├────────────────────────────────────────────────────────────────────────────┤
│ Sources tab                                                                │
│ sources & scope, trust checks                                              │
└────────────────────────────────────────────────────────────────────────────┘
```

## API mapping

| UI concept | API/tool backing | Notes |
|---|---|---|
| Search field | `POST /api/search_traces` | Uses `query` plus optional role/project/source/machine/path, then enriches grouped hits into trace cards. |
| Search in: Prompts | `role = "user"` | Default search mode; prompt recall comes first. |
| Search in: All history | omit `role` | Search messages and artefacts. |
| Search in: Assistant replies | `role = "assistant"` | Searches assistant-authored messages. |
| Search in: Tool output | `role = "toolResult"` | Searches indexed tool-result messages. |
| Trace cards | grouped enriched search hits | Server groups hits by `session_key`, adds counts, timeline, command chips, file chips, status, and matched events. |
| Evidence | `POST /api/read` | Uses the clicked card's `ref_text`. |
| Recurring failures | `POST /api/incidents` | State labels map to corpus states. |
| Trace fix | `POST /api/incident_trajectory` | Requires sample ref and ordinal. |
| Sources & scope | `GET /api/overview` | Counts and scope chips. |
| Trust checks | `GET /api/conflicts` | Quarantined rows. |

## Interaction rules

- Top-level tabs separate user journeys: Search, Failures, Sources.
- Empty state is not a task rail. It tells the user to search and explains trace-card output.
- The search box searches prompts by default. All history is one chip away.
- Search chips set the hidden role filter, update `aria-pressed`, and rerun the search when a query is present.
- Advanced filters are secondary and collapsed by default.
- Scope changes show a visible summary and a live feedback sentence so users know what changed.
- Search results come back as enriched trace cards grouped by session key.
- Selecting any trace card loads the first matched ref in **Evidence**, shows the selected trace header, enables copy-ref/widen-context actions, and highlights the selected entry when possible.
- Overview chips populate search and incident facets, then focus the search box.
- Incident rows and fix paths continue to drill into read context.
- Clipboard actions are user-initiated only.

## Copy vocabulary

Prefer:

- Search agent history
- trace cards
- Search
- Failures
- Sources
- Search in
- Prompts
- All history
- Assistant replies
- Tool output
- Advanced filters
- Evidence
- Recurring failures
- needs attention
- sometimes fixed
- fixed before
- copy fix notes
- Sources & scope
- Trust checks

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
- Use tinted OKLCH neutrals and restrained semantic colour. Text colours must not use pure black or pure white.
- Every clickable control has visible effect: active state, live feedback, changed results, changed scope, or opened context.
- Prefer spacing, grouping, and labels over extra instructional cards.

## Layout requirements

Desktop:

- Top-level tabs sit immediately under the app header.
- The Search tab is the default workspace.
- The compact search surface spans the full width and appears first inside Search.
- Search input is large, but its label and hint are compact enough that results stay close to the fold.
- Trace cards and Evidence sit in one explicit workbench grid so their edges align.
- The reader is not sticky; sticky layering can overlap later sections and make scroll state ambiguous.
- A compact Search-tab hint names the local hierarchy: Trace or Conversation, Event, Evidence.
- Failures and Sources are not visible in the Search workspace except as top-level tabs.

Mobile/narrow:

- Tabs wrap if needed.
- Search tab single-column order: search, domain model hint, trace cards, evidence.
- Failures and Sources keep their own single-column layouts.
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
- It uses only same-origin local endpoints served by `aha dashboard`.
- It does not fetch remote assets.
- It does not snapshot, ingest, repair, edit source histories, write skills, or install anything.
- Search/read output remains governed by the corpus projection and existing redaction behaviour.
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
