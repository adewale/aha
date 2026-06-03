# CLAUDE.md

Load and use the contents of agents.md. The import below pulls it into context
automatically at session start (the prose alone is not enough — Claude Code only
expands files referenced with the `@` import syntax).

@agents.md

`agents.md` defines the non-negotiable implementation principles for this
repository (red-green-refactor TDD and correctness-by-construction). Follow it
for every change.
