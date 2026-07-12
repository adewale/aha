# TODO

## 0.2 follow-up

- Complete live R2 journey evidence for the new `archive` surface: existing shared Archive upload, pull-only aggregation, repeated no-op download, denied credentials, unsafe destination, cancellation, and deep verification.
- Rename the remaining internal `depot` and `corpus` package/schema vocabulary only through explicit storage-format migrations; public 0.2 vocabulary is already Archive/Workspace.
- Add a config compatibility date only alongside the first real behaviour gate; define its immutable change registry, minimum supported date, upgrade command, and rollback policy together.
- Add committed N/N+1 golden fixtures and a separately built older-binary writer test for Archive, Workspace, config, HTTP, and MCP compatibility boundaries.
- Add release packaging that calls `make build` so commit/time/dirty metadata is always linker-injected.

## Retrieval and processing

- Add batch `show` for workflows that need context for several search refs.
- Explore structured/saved searches, hybrid ranking, query expansion, and ranking explanations; semantic/vector search remains outside 0.2.
- Keep `--refs`, `--files`, and `--md` output contracts stable.
- Continue outcome-weighted failure analysis and trajectory quality work without turning every recurring pattern into a generated skill.

## Performance and longevity

- Follow `docs/performance-scalability-plan.md` using scenario metrics and package-level profiles before optimisation.
- Keep pathological Workspace verify/FTS query-plan guards green.
- Continue byte/call accounting for Archive upload/download and known-content anti-entropy.
- Add internal automatic SQLite maintenance only when measured evidence justifies it; do not reintroduce a public optimise command.

## Trust and verification

- Keep source roots, production R2 credentials, and smoke destinations isolated by construction.
- Extend the live smoke attestation only through source-pinned test capabilities.
- Preserve Linux, Darwin, and Windows build checks; Workspace repair remains explicitly unsupported where safe atomic exchange is unavailable.
- Continue mutation testing on lifecycle transitions, capability zero values, destination preflight, publication CAS, and error normalisation.
