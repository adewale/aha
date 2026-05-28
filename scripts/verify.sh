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
  run go test ./internal/mcp -run=^$ -fuzz=FuzzParseFrames -fuzztime="$FUZZTIME"
  run go test ./internal/mcp -run=^$ -fuzz=FuzzEncodeParseRoundTrip -fuzztime="$FUZZTIME"
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

# mcp runs the bidirectional MCP conformance suite. Two scenarios:
#   1. The official MCP Python SDK client drives `aha mcp` and asserts
#      wire-level conformance (handshake, tools/list shape, structuredContent,
#      readOnlyHint annotation, strict arg validation).
#   2. Our TS stdio transport drives a tiny FastMCP reference server and
#      round-trips three tool calls — proving the *client* speaks real MCP
#      to a known-good server, not just to itself.
# Skips gracefully when python3 or the `mcp` package isn't available.
mcp_conformance() {
  if ! command -v python3 >/dev/null 2>&1; then
    printf '\n==> mcp conformance: skipped (python3 not found)\n' >&2
    return 0
  fi
  if ! python3 -c "import mcp" 2>/dev/null; then
    printf '\n==> mcp conformance: skipped (python3 -m pip install mcp[cli] to enable)\n' >&2
    return 0
  fi
  # Build the binary if it isn't already there.
  if [[ ! -x /tmp/aha ]]; then
    run go build -o /tmp/aha ./cmd/aha
  fi
  # Phase 1: official Python SDK client → aha mcp
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
  AHA_BIN=/tmp/aha AHA_CONFIG="$tmpdir/config.jsonc" \
    run python3 scripts/mcp-conformance/client_against_aha.py
  # Phase 2: aha TS client → official Python SDK reference server
  if command -v node >/dev/null 2>&1 && command -v tsc >/dev/null 2>&1; then
    AHA_REF_SERVER="python3 $PWD/scripts/mcp-conformance/reference_server.py" \
      run node --experimental-strip-types --test clients/typescript/test/stdio.conformance.test.ts
  else
    printf '\n==> mcp conformance phase 2: skipped (need node + tsc)\n' >&2
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
