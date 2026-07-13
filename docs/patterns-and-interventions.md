# Patterns and interventions

`aha` finds recurring patterns in local coding-agent history. The right response is not always a skill. Sometimes the best artefact is a runbook, a dynamic workflow, a tool/platform fix, or an investigation backlog item.

Use this guide to manually turn `aha` incidents and evidence refs into the right artefact.

## What `aha` gives you today

`aha` exposes the pattern substrate through several read-only surfaces:

- CLI: `aha analyse failures`, `aha search`, `aha show`.
- Dashboard: `aha dashboard` → **Failures** for recurring failures, **Search** for trace/evidence review, **Sources** for scope/trust.
- MCP: `analyse_failures`, `analyse_failure_trajectory`, `search`, `show`, `overview`, `status`, `workspace_verify`, `workspace_conflicts`, `workspace_size`, `aha_capabilities`.
- TypeScript client: `clients/typescript/` wrappers for MCP/HTTP/code-mode runtimes.

`aha` does **not** currently choose or write the final artefact for you. It gives you ranked incidents, normalized fix paths, and stable evidence refs so a human or agent can decide what to create.

## Artefact taxonomy

| Pattern shape | Prefer | Why | Example |
|---|---|---|---|
| Deterministic operational sequence | Runbook | The answer is a repeatable checklist or command order. | GitHub CI triage: list runs → inspect logs → fix → rerun/check → merge. |
| Reusable judgment, habit, or review lens | Skill | The answer is when/how to think, inspect, or critique. | Exact edit hygiene; testing quality review; frontend slop avoidance. |
| Broad, parallel, high-uncertainty work | Dynamic workflow | The answer needs fan-out, independent attempts, synthesis, or adversarial review. | Large codebase audit; multi-angle PR review; dead-code discovery. |
| Repeated tool friction with narrow fixability | Tool/platform fix | The answer should remove the failure mode from the system. | Better `show` bounds, safer edit previews, card layout containment tests. |
| High-pain pattern with weak or missing fix evidence | Investigation backlog | The pattern is real, but the remedy is not proven yet. | Repeated unresolved browser flake or merge conflict with no reliable path. |

## Decision rules

Start from the incident, then ask what kind of intervention would prevent or shorten the next recurrence.

### Choose a runbook when

- The pattern has a stable sequence of commands or checks.
- Most value comes from doing steps in the right order.
- The workflow is operational rather than judgment-heavy.
- The evidence shows a fix path that worked more than once.

Common signals:

- `gh run list`, `gh run view`, `gh pr checks`, `gh run rerun`.
- Deploy verification, release checks, R2 setup, package publish steps.
- Repeated “check status → inspect failure → repair → verify” loops.

### Choose a skill when

- The pattern depends on judgment, taste, or discipline.
- The user wants the agent to notice something earlier next time.
- The guidance should be loaded only when a task matches its description.
- The fix is not just one command; it is a way to work.

Common signals:

- repeated exact-edit failures;
- weak tests or over-mocking;
- UI critique/slop patterns;
- recurring documentation drift;
- security/privacy review habits.

### Choose a dynamic workflow when

- The task benefits from parallel subagents or independent attempts.
- The scope is broad enough that one linear pass is unreliable.
- You want adversarial review before accepting an answer.
- The final answer should synthesize several partial analyses.

Common signals:

- “audit this branch from safety/docs/tests/product angles”;
- “find dead code across this repository”;
- “compare many incidents and infer root causes”;
- “review a large refactor with independent critics.”

Dynamic workflows are usually too expensive for simple command loops. Prefer a runbook when the sequence is deterministic.

### Choose a tool/platform fix when

- The same low-level failure repeats regardless of project.
- A better default, validation rule, UI affordance, or regression test can make the bad state hard to construct.
- The “lesson” is mostly mechanical.

Common signals:

- `show` offset errors;
- `edit oldText` mismatch patterns;
- shell quoting failures that a wrapper could avoid;
- dashboard cards overflowing or overlaying neighbors;
- repeated JSON contract drift that a generated test could catch.

### Choose an investigation backlog item when

- The pattern has high recurrence but low resolution rate.
- Top paths have weak support or no sample ref.
- The incidents are too generic to form a safe recommendation.
- The evidence suggests several unrelated root causes.

Backlog items should include hypotheses and evidence refs, not a premature fix.

## Manual extraction workflow

### 1. Check corpus scope

```bash
aha status --json
aha workspace verify --json
```

Use `status` to see Workspace size, sources, redaction levels, and whether the data is large enough to trust. Use `aha workspace verify` before drawing conclusions from an old Workspace.

### 2. Find high-pain patterns

```bash
aha analyse failures --limit 50 --json
aha analyse failures --state unresolved --limit 25 --json
aha analyse failures --state resolved --limit 25 --json
```

Interpret states as:

- `unresolved`: likely investigation backlog or platform fix candidate.
- `partial`: candidate for runbook/skill if paths are coherent; otherwise investigation.
- `resolved`: best starting point for runbooks and skills, because there is at least one observed fix path.

### 3. Scope to a project/source/tool

```bash
aha analyse failures --project myrepo --json
aha analyse failures --tool bash --json
aha analyse failures --source claude-code --json
```

Scope when a pattern looks too broad. A generic `edit` or `rg` incident may become meaningful once scoped to a project or workflow.

### 4. Read evidence

Every incident carries a `sample_ref`. Resolved incidents may also include path refs.

```bash
aha show 'msg:v1:...' --before 3 --after 10 --md
```

Treat search snippets and incident summaries as leads. Treat `show` output as evidence.

### 5. Inspect the fix trajectory

For a resolved path, use `sample_ref` plus `sample_ordinal` with the MCP/HTTP `analyse_failure_trajectory` surface. From the dashboard, click **trace** on a fix path.

For agents using MCP, call:

```json
{
  "tool": "incident_trajectory",
  "args": {
    "ref": "msg:v1:...",
    "ordinal": 0
  }
}
```

The trajectory is the fail→fix arc: the failing invocation, intermediate command families, and the resolving success.

### 6. Decide the artefact

Use this quick classifier:

```text
Is there a stable ordered command sequence?
  yes → runbook
  no  → continue

Does it teach reusable judgment or taste?
  yes → skill
  no  → continue

Would parallel subagents materially improve the next run?
  yes → dynamic workflow
  no  → continue

Can the system make the bad state impossible or obvious?
  yes → tool/platform fix
  no  → investigation backlog
```

## Artefact templates

### Runbook

```md
# <runbook name>

## Use when

- <trigger condition>

## Inputs

- <repo / PR / run ID / ref>

## Steps

1. <inspect status>
2. <inspect evidence/logs>
3. <apply repair>
4. <verify locally>
5. <verify remotely>

## Stop conditions

- <when to stop or escalate>

## Evidence

- <aha ref>
- <aha ref>
```

### Skill

```md
# <skill name>

## When to use

Use when <task or failure pattern>.

## Guidance

- <judgment rule>
- <workflow habit>
- <verification rule>

## Common failure modes

- <what agents tend to miss>

## Evidence

- <aha ref>
- <aha ref>
```

### Dynamic workflow

```md
# <workflow name>

## Goal

<large task that benefits from fan-out>

## Phases

1. Scope and inventory.
2. Parallel analysis branches.
3. Synthesis.
4. Adversarial review.
5. Final recommendation with evidence.

## Suggested subagents

- <agent 1>
- <agent 2>
- <critic / verifier>

## Acceptance evidence

- <tests / reports / refs required before completion>
```

For Pi dynamic workflows, this maps naturally to `phase(...)`, `agent(...)`, `parallel(...)`, and `pipeline(...)` calls in a workflow script.

### Tool/platform fix

```md
# <fix proposal>

## Repeated failure

- Episodes: <n>
- Sessions: <n>
- Projects: <n>
- Evidence refs: <refs>

## Root cause hypothesis

<what the tool/UI/API allowed that it should prevent>

## Proposed system change

- <validation/default/test/UI change>

## Regression test

- <test that fails before and passes after>
```

### Investigation backlog item

```md
# Investigate <pattern>

## Signal

- Episodes: <n>
- Sessions: <n>
- Projects: <n>
- Current resolution rate: <rate>

## Evidence

- <aha ref>
- <aha ref>

## Hypotheses

1. <possible root cause>
2. <possible root cause>

## Next experiment

<smallest read-only or reversible check that would disambiguate>
```

## Suggested agent prompt

When asking an agent to do this manually today, use:

```text
Use aha show-only surfaces only. Inspect `aha analyse failures --json`, sample evidence with `aha show`, and classify the top patterns into runbook, skill, dynamic workflow, tool/platform fix, or investigation backlog. Do not write or install artifacts yet. For each recommendation include stats, why this artifact type fits, and evidence refs.
```

## Current limits

- `aha` does not yet expose `patterns_suggest`, `pattern_evidence`, or `intervention_draft`.
- Incident grouping is by normalized tool/command/error, so humans or agents still need to synthesize broader themes.
- Tool output is privacy-preserved in the incident surface; read the raw evidence refs only when needed and only in a trusted context.
- Dynamic workflows are external to `aha`; `aha` can provide evidence and candidate tasks, not execute them.
