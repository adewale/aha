# Verification Guide

This project keeps one verification entrypoint in `scripts/verify.sh`, with Make aliases for convenience.

## Common commands

```bash
make verify-quick       # go test ./... + whitespace checks
make verify-full        # quick + vet + race + fuzz + build
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

That profile currently includes:

- `go test ./...`
- whitespace checks for the PR/commit diff and local worktree
- `go vet ./...`
- `go test -race ./...`
- bounded fuzz targets for model refs, archive walk/write, adapter JSONL parsing, depot addresses, and depot bundle keys;
- `go build -o /tmp/aha ./cmd/aha`

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
aha verify --json
aha verify --repair-fts --json
```

`verify` is read-only by default. `--repair-fts` rebuilds derived FTS rows from `messages` and `artifacts`; raw bundle/corpus rows remain the source of truth.

## Optional profiling

For performance investigations, opt in to local Go pprof profiles on any command:

```bash
aha --cpuprofile cpu.pprof --memprofile heap.pprof verify --repo ~/.aha/corpus
AHA_CPU_PROFILE=cpu.pprof AHA_MEM_PROFILE=heap.pprof aha refresh
```

Profiles are never written by default. Inspect them with `go tool pprof`.

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
