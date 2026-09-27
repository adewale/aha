# Verification Guide

This project keeps one verification entrypoint in `scripts/verify.sh`, with Make aliases for convenience.

## Common commands

```bash
make verify-quick       # go test ./... + whitespace checks
make verify-full        # the CI profile below
make verify-fuzz        # bounded fuzz suite
make verify-mutation-dry
make verify-mutation
```

Equivalent direct usage:

```bash
scripts/verify.sh quick
scripts/verify.sh full
scripts/verify.sh fuzz
scripts/verify.sh mutation-dry
```

`FUZZTIME` controls the per-target fuzz budget:

```bash
FUZZTIME=10s scripts/verify.sh fuzz
```

## CI profile

GitHub Actions runs:

```bash
scripts/verify.sh ci
```

That profile (`full()` in `scripts/verify.sh`) runs these steps in order. The
list is kept in sync with `full()` by
`TestVerificationDocListsEveryCIProfileStep` in `internal/testquality`:

- `quick`: `go test ./...` plus whitespace checks for the PR/commit diff and local worktree
- `go vet ./...`
- `go test -race ./...`
- `fuzz`: every `func Fuzz*` target for `FUZZTIME` (default 2s) each; `TestVerifyFuzzListMatchesFuzzTargetsInBothDirections` fails if a target is missing from the list
- `ts`: typecheck and runtime-test the generated TypeScript client (fails if its locked dependencies are not installed)
- `build_private`: build `cmd/aha` in a private temporary workspace
- `./scripts/compat-n-minus-one.sh`: the pinned previous release must read data written by the current binary, and vice versa
- `cross_compile`: build for Linux, macOS and Windows, and compile the Windows platform-contract test binaries
- `mcp_conformance`: the eight cross-SDK MCP conformance legs (Python, TypeScript and Go SDK clients against `aha mcp`; aha's TS client against Python, TS and Go reference servers; the Code Mode workflow; HTTP/MCP consistency)

CI installs the conformance dependencies (`npm ci --prefix scripts/mcp-conformance` and
`pip install -r scripts/mcp-conformance/requirements.txt`) and sets
`AHA_MCP_REQUIRE_ALL_LEGS=1`, so a conformance leg that cannot run fails the build.
Locally, legs whose toolchain is missing are skipped and reported as skipped;
set `AHA_MCP_REQUIRE_ALL_LEGS=1` to reproduce CI.

## Correctness-by-construction guardrails

`internal/testquality` contains static tests that freeze known correctness debt before refactors begin. The goal is to prevent debt from spreading, then shrink the allowlists as construction mechanisms replace conventions.

Current inventories include:

- ambient time and sleep sites;
- raw identity-key concatenation sites;
- direct FTS write sites;
- focused/sleep/log-only test anti-patterns.

When a refactor removes a debt item, update the static allowlist in the same commit. When all items in a category are removed, replace the inventory with a hard ban.

## Runtime corpus verification

Use the CLI verifier against a real corpus when diagnosing local drift:

```bash
aha workspace verify --json
aha workspace verify --repair-fts --json
```

`verify` is read-only by default and JSON output includes corpus/FTS row-count stats so humans and agents can see what was checked. `--repair-fts` rebuilds derived FTS rows from `messages` and `artifacts` and reports deleted/inserted FTS row counters; depot blobs and base corpus rows remain the source of truth.

## Optional profiling

For performance investigations, opt in to local Go pprof profiles on any command:

```bash
aha --cpuprofile cpu.pprof --memprofile heap.pprof verify --workspace ~/.aha/corpus
AHA_CPU_PROFILE=cpu.pprof AHA_MEM_PROFILE=heap.pprof aha archive upload && aha archive download
```

Profiles are never written by default. Inspect them with `go tool pprof`. Captured package-level benchmark profile summaries live in `docs/performance-results.md`; prefer those small reproductions before command-level profiling.

## Mutation testing

Mutation testing is intentionally not part of normal CI. Run it before release or after invariant-critical refactors:

```bash
make verify-mutation-dry
make verify-mutation
```

The script uses:

```bash
go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash <pkg>
```

Critical packages:

- `./internal/model`
- `./internal/corpus`
- `./internal/archive`
- `./internal/depot`
- `./internal/adapters`

A surviving mutant in ref parsing, archive validation, depot key validation, path safety, or conflict quarantine should be treated as a release blocker unless it is a documented equivalent mutant.
