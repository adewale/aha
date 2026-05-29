#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: scripts/verify.sh MODE

Modes:
  quick         go test ./... plus committed/local whitespace checks
  full          quick + go vet + race + build + bounded fuzz suite
  ci            same as full; intended for GitHub Actions
  fuzz          run all bounded fuzz targets
  race          run race detector
  build         build cmd/aha into /tmp/aha
  ts            typecheck + runtime-test the TypeScript client (skips if no toolchain)
  mcp           bidirectional MCP conformance: aha server vs official Python SDK client, AND aha TS client vs official Python SDK server (skips if python3 mcp not installed)
  mutation-dry  inventory covered mutants with gremlins dry-run
  mutation      run gremlins against invariant-critical packages

Environment:
  FUZZTIME      fuzz duration per target (default: 2s)
  GREMLINS     gremlins command (default: go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0)
USAGE
}

mode="${1:-}"
if [[ -z "$mode" || "$mode" == "-h" || "$mode" == "--help" ]]; then
  usage
  exit 0
fi

FUZZTIME="${FUZZTIME:-2s}"
GREMLINS="${GREMLINS:-go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0}"

run() {
  printf '\n==> %s\n' "$*" >&2
  "$@"
}

run_shell() {
  printf '\n==> %s\n' "$*" >&2
  bash -lc "$*"
}

whitespace_check() {
  if [[ -n "${GITHUB_BASE_REF:-}" ]]; then
    git fetch --no-tags --depth=1 origin "${GITHUB_BASE_REF}" >/dev/null 2>&1 || true
    base="$(git merge-base HEAD "origin/${GITHUB_BASE_REF}" 2>/dev/null || true)"
    if [[ -n "$base" ]]; then
      run git diff --check "$base"...HEAD
      return
    fi
  fi
  if git rev-parse --verify HEAD^ >/dev/null 2>&1; then
    run git diff --check HEAD^..HEAD
  fi
  run git diff --check
}

quick() {
  run go test ./...
  whitespace_check
}

fuzz() {
  run go test ./internal/model -run=^$ -fuzz=FuzzRefParseFormat -fuzztime="$FUZZTIME"
  run go test ./internal/archive -run=^$ -fuzz=FuzzWalkBundleRoundTrip -fuzztime="$FUZZTIME"
  run go test ./internal/adapters -run=^$ -fuzz=FuzzParseGenericJSONL -fuzztime="$FUZZTIME"
  run go test ./internal/depot -run=^$ -fuzz=FuzzParseAddress -fuzztime="$FUZZTIME"
  run go test ./internal/depot -run=^$ -fuzz=FuzzValidateBundleKey -fuzztime="$FUZZTIME"
  # The MCP framer is owned by github.com/modelcontextprotocol/go-sdk and
  # fuzzed in that repo's mcp/*_test.go. Wire-format regressions land in
  # the conformance suite (`scripts/verify.sh mcp`) before they could
  # ship.
}

# ts typechecks the generated TypeScript client surface and runs its runtime
# tests. It is optional: if a TypeScript toolchain is not installed the step
# is skipped with a notice rather than failing, so Go-only environments and
# CI without Node still pass `full`.
ts() {
  local dir="clients/typescript"
  if ! command -v tsc >/dev/null 2>&1; then
    printf '\n==> ts: skipped (tsc not found)\n' >&2
    return 0
  fi
  run_shell "cd '$dir' && tsc --noEmit"
  if command -v node >/dev/null 2>&1; then
    run node --experimental-strip-types --test "$dir/test/stdio.test.ts"
  else
    printf '\n==> ts runtime tests: skipped (node not found)\n' >&2
  fi
}

# mcp runs the cross-SDK conformance suite. Six legs across three SDKs
# (Python `mcp`, TypeScript `@modelcontextprotocol/sdk`, Go
# `github.com/modelcontextprotocol/go-sdk`):
#
#   server (aha mcp) is driven by ...
#     1. Python SDK client      -> scripts/mcp-conformance/client_against_aha.py
#     2. TypeScript SDK client  -> scripts/mcp-conformance/client_against_aha.ts
#     3. Go SDK client          -> internal/mcp/conformance/go_sdk_test.go
#
#   our TS client is driven against ...
#     4. Python SDK reference server (FastMCP)
#     5. TypeScript SDK reference server (McpServer)
#     6. Go SDK reference server (cmd/aha-ref-mcp)
#
# Each leg skips gracefully when its toolchain is missing, so this mode
# still does *something* useful on a Python-only or Go-only box.
mcp_conformance() {
  # The Go-SDK leg needs no external toolchain — it runs under `go test`.
  # The Python and TS legs need their respective SDKs installed.
  local have_python=0 have_node=0 have_tsc=0
  command -v python3 >/dev/null 2>&1 && python3 -c "import mcp" 2>/dev/null && have_python=1
  command -v node    >/dev/null 2>&1 && have_node=1
  command -v tsc     >/dev/null 2>&1 && have_tsc=1

  # Build aha + the Go-SDK reference server once.
  if [[ ! -x /tmp/aha ]]; then
    run go build -o /tmp/aha ./cmd/aha
  fi
  if [[ ! -x /tmp/aha-ref-mcp ]]; then
    run go build -o /tmp/aha-ref-mcp ./cmd/aha-ref-mcp
  fi

  # Make sure the TS SDK + zod are installed in scripts/mcp-conformance/ if
  # node is available. Skip the install if package-lock is fresh enough.
  if (( have_node )) && [[ ! -d scripts/mcp-conformance/node_modules ]]; then
    run_shell "cd scripts/mcp-conformance && npm install --silent --no-audit --no-fund"
  fi

  # Populate a fixture corpus the Python+TS+Go clients can drive against.
  local tmpdir; tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' RETURN
  local pi="$tmpdir/pi/--Users-me-proj--"
  mkdir -p "$pi"
  cat > "$pi/2026.jsonl" <<'JSONL'
{"type":"session","version":3,"id":"pi-session","timestamp":"2026-01-01T00:00:00Z","cwd":"/Users/me/proj"}
{"id":"p1","parentId":"","type":"user","role":"user","timestamp":"2026-01-01T00:00:01Z","message":{"content":"hello"}}
JSONL
  cat > "$tmpdir/config.jsonc" <<JSONC
{
  "machine_id":"mcp-conformance",
  "sources":[{"type":"pi","root":"$pi","enabled":true}],
  "corpus_dir":"$tmpdir/corpus",
  "depot":{"type":"local","location":"$tmpdir/depot"},
  "accept_secrets_warning":true
}
JSONC
  run /tmp/aha refresh --config "$tmpdir/config.jsonc" --captured-at 2026-01-01T00:00:00Z --bundle-id conformance >/dev/null

  # ---- Server-under-test legs (drive aha mcp from a real SDK Client) ----

  if (( have_python )); then
    AHA_BIN=/tmp/aha AHA_CONFIG="$tmpdir/config.jsonc" \
      run python3 scripts/mcp-conformance/client_against_aha.py
  else
    printf '\n==> mcp leg 1 (python client -> aha): skipped (python3 mcp not available)\n' >&2
  fi

  if (( have_node )); then
    AHA_BIN=/tmp/aha AHA_CONFIG="$tmpdir/config.jsonc" \
      run_shell "cd scripts/mcp-conformance && node --experimental-strip-types client_against_aha.ts"
  else
    printf '\n==> mcp leg 2 (typescript client -> aha): skipped (node not available)\n' >&2
  fi

  AHA_BIN=/tmp/aha AHA_CONFIG="$tmpdir/config.jsonc" \
    run go test -count=1 ./internal/mcp/conformance/...

  # ---- Code Mode workflow: typed surface drives search -> filter -> read ----
  if (( have_node )); then
    AHA_BIN=/tmp/aha AHA_CONFIG="$tmpdir/config.jsonc" \
      run_shell "cd scripts/mcp-conformance && node --experimental-strip-types codemode_workflow.ts"
  else
    printf '\n==> mcp code-mode workflow: skipped (node not available)\n' >&2
  fi

  # ---- Client-under-test legs (drive our TS Transport against real servers) ----

  if (( have_node && have_tsc )); then
    local ref_env=""
    if (( have_python )); then
      ref_env+=" AHA_REF_SERVER=\"python3 $PWD/scripts/mcp-conformance/reference_server.py\""
    fi
    ref_env+=" AHA_REF_SERVER_TS=\"node --experimental-strip-types $PWD/scripts/mcp-conformance/reference_server.ts\""
    ref_env+=" AHA_REF_SERVER_GO=\"/tmp/aha-ref-mcp\""
    run_shell "env $ref_env node --experimental-strip-types --test clients/typescript/test/stdio.conformance.test.ts"
  else
    printf '\n==> mcp legs 4-6 (aha client -> reference servers): skipped (need node + tsc)\n' >&2
  fi
}

full() {
  quick
  run go vet ./...
  run go test -race ./...
  fuzz
  ts
  run go build -o /tmp/aha ./cmd/aha
  mcp_conformance
}

mutation_packages=(
  ./internal/model
  ./internal/corpus
  ./internal/archive
  ./internal/depot
  ./internal/adapters
)

mutation_dry() {
  for pkg in "${mutation_packages[@]}"; do
    run_shell "$GREMLINS unleash '$pkg' --dry-run --workers 2"
  done
}

mutation() {
  for pkg in "${mutation_packages[@]}"; do
    run_shell "$GREMLINS unleash '$pkg' --workers 2"
  done
}

case "$mode" in
  quick) quick ;;
  full|ci) full ;;
  fuzz) fuzz ;;
  ts) ts ;;
  mcp) mcp_conformance ;;
  race) run go test -race ./... ;;
  build) run go build -o /tmp/aha ./cmd/aha ;;
  mutation-dry) mutation_dry ;;
  mutation) mutation ;;
  *) usage >&2; exit 2 ;;
esac
