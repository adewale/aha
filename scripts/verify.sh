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
  build         compile cmd/aha in a private temporary workspace
  ts            typecheck + runtime-test the TypeScript client (skips if no toolchain)
  mcp           isolated bidirectional MCP conformance (never installs dependencies or uses ambient aha config)
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

cleanup_dirs=()
cleanup() {
  local dir
  if (( ${#cleanup_dirs[@]} == 0 )); then
    return
  fi
  for dir in "${cleanup_dirs[@]}"; do
    rm -rf -- "$dir"
  done
}
trap cleanup EXIT HUP INT TERM

new_workspace() {
  NEW_WORKSPACE="$(mktemp -d "${TMPDIR:-/tmp}/aha-verify.XXXXXX")"
  chmod 700 "$NEW_WORKSPACE"
  cleanup_dirs+=("$NEW_WORKSPACE")
}

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

build_private() {
  new_workspace
  run go build -o "$NEW_WORKSPACE/aha" ./cmd/aha
}

cross_compile() {
  new_workspace
  local root="$NEW_WORKSPACE"
  run env GOOS=linux GOARCH=amd64 go build -o "$root/aha-linux-amd64" ./cmd/aha
  run env GOOS=darwin GOARCH=amd64 go build -o "$root/aha-darwin-amd64" ./cmd/aha
  run env GOOS=windows GOARCH=amd64 go build -o "$root/aha-windows-amd64.exe" ./cmd/aha
  # Cross-running test binaries is impossible, and compiling all *_test.go
  # currently reaches platform-specific syscall tests. Building the complete
  # command graph still gates production portability without broad test-only
  # platform refactors.
}

fuzz() {
  run go test ./internal/model -run=^$ -fuzz=FuzzRefParseFormat -fuzztime="$FUZZTIME"
  run go test ./internal/archive -run=^$ -fuzz=FuzzWalkBundleRoundTrip -fuzztime="$FUZZTIME"
  run go test ./internal/adapters -run=^$ -fuzz=FuzzParseGenericJSONL -fuzztime="$FUZZTIME"
  run go test ./internal/depot -run=^$ -fuzz=FuzzDecodeLatestPointer -fuzztime="$FUZZTIME"
  run go test ./internal/depot -run=^$ -fuzz=FuzzDecodeMachinesIndex -fuzztime="$FUZZTIME"
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
  if command -v tsc >/dev/null 2>&1; then
    run_shell "cd '$dir' && tsc --noEmit"
  else
    printf '\n==> ts typecheck: skipped (tsc not found)\n' >&2
  fi
  if command -v node >/dev/null 2>&1; then
    run node --experimental-strip-types --test "$dir/test/stdio.test.ts"
  else
    printf '\n==> ts runtime tests: skipped (node not found)\n' >&2
  fi
}

# mcp runs the cross-SDK conformance suite. Six SDK legs across three SDKs
# (Python `mcp`, TypeScript `@modelcontextprotocol/sdk`, Go
# `github.com/modelcontextprotocol/go-sdk`), plus a Code Mode workflow leg
# and an in-process HTTP<->MCP consistency check (run via go test below):
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
#   plus ...
#     7. Code Mode workflow     -> scripts/mcp-conformance/codemode_workflow.ts
#     8. HTTP<->MCP consistency -> internal/mcp TestHTTPAndMCPPathsAreConsistent
#
# Each leg skips gracefully when its toolchain is missing, so this mode
# still does *something* useful on a Python-only or Go-only box.
mcp_conformance() {
  # The Go-SDK leg needs no external toolchain. Python/TS legs run only when
  # their already-prepared dependencies are present; verification never runs
  # a package manager or mutates the checkout.
  local have_python=0 have_node=0 have_tsc=0 have_ts_sdk=0
  command -v python3 >/dev/null 2>&1 && python3 -c "import mcp" 2>/dev/null && have_python=1
  command -v node    >/dev/null 2>&1 && have_node=1
  command -v tsc     >/dev/null 2>&1 && have_tsc=1
  if (( have_node )) && [[ -d scripts/mcp-conformance/node_modules/@modelcontextprotocol/sdk ]]; then
    have_ts_sdk=1
  fi

  new_workspace
  local root="$NEW_WORKSPACE"
  local aha_bin="$root/bin/aha"
  local ref_bin="$root/bin/aha-ref-mcp"
  local cfg="$root/config.jsonc"
  local token="aha-mcp-$PPID-$RANDOM-$RANDOM"
  mkdir -p "$root/bin"
  printf '%s\n' "$token" > "$root/.aha-mcp-conformance"
  chmod 600 "$root/.aha-mcp-conformance"

  run go build -o "$aha_bin" ./cmd/aha
  run go build -o "$ref_bin" ./cmd/aha-ref-mcp

  local pi="$root/pi/--Users-me-proj--"
  mkdir -p "$pi"
  cat > "$pi/2026.jsonl" <<'JSONL'
{"type":"session","version":3,"id":"pi-session","timestamp":"2026-01-01T00:00:00Z","cwd":"/Users/me/proj"}
{"id":"p1","parentId":"","type":"user","role":"user","timestamp":"2026-01-01T00:00:01Z","message":{"content":"hello"}}
JSONL
  cat > "$cfg" <<JSONC
{
  "machine_id":"mcp-conformance",
  "sources":[{"type":"pi","root":"$pi","enabled":true}],
  "corpus_dir":"$root/corpus",
  "depot":{"type":"local","location":"$root/depot"},
  "accept_secrets_warning":true
}
JSONC
  run "$aha_bin" refresh --config "$cfg" --captured-at 2026-01-01T00:00:00Z >/dev/null

  local -a attested_env=(
    "AHA_BIN=$aha_bin"
    "AHA_CONFIG=$cfg"
    "AHA_MCP_CONFORMANCE_ROOT=$root"
    "AHA_MCP_CONFORMANCE_TOKEN=$token"
  )

  if (( have_python )); then
    run env "${attested_env[@]}" python3 scripts/mcp-conformance/client_against_aha.py
  else
    printf '\n==> mcp leg 1 (python client -> aha): skipped (python3 mcp not available)\n' >&2
  fi

  if (( have_ts_sdk )); then
    run_shell "cd scripts/mcp-conformance && env AHA_BIN='$aha_bin' AHA_CONFIG='$cfg' AHA_MCP_CONFORMANCE_ROOT='$root' AHA_MCP_CONFORMANCE_TOKEN='$token' node --experimental-strip-types client_against_aha.ts"
  else
    printf '\n==> mcp leg 2 (typescript client -> aha): skipped (prepared TS SDK dependencies not available; prepare scripts/mcp-conformance dependencies explicitly)\n' >&2
  fi

  run env "${attested_env[@]}" go test -count=1 ./internal/mcp/conformance/...

  if (( have_ts_sdk )); then
    run_shell "cd scripts/mcp-conformance && env AHA_BIN='$aha_bin' AHA_CONFIG='$cfg' AHA_MCP_CONFORMANCE_ROOT='$root' AHA_MCP_CONFORMANCE_TOKEN='$token' node --experimental-strip-types codemode_workflow.ts"
  else
    printf '\n==> mcp code-mode workflow: skipped (prepared TS SDK dependencies not available)\n' >&2
  fi

  if (( have_node && have_tsc )); then
    local -a ref_env=()
    if (( have_python )); then
      ref_env+=("AHA_REF_SERVER=python3 $PWD/scripts/mcp-conformance/reference_server.py")
    fi
    if (( have_ts_sdk )); then
      ref_env+=("AHA_REF_SERVER_TS=node --experimental-strip-types $PWD/scripts/mcp-conformance/reference_server.ts")
    fi
    ref_env+=("AHA_REF_SERVER_GO=$ref_bin")
    run env "${ref_env[@]}" node --experimental-strip-types --test clients/typescript/test/stdio.conformance.test.ts
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
  build_private
  cross_compile
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
  build) build_private ;;
  mutation-dry) mutation_dry ;;
  mutation) mutation ;;
  *) usage >&2; exit 2 ;;
esac
