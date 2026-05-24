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
  run go test ./internal/model -run=^$ -fuzz=FuzzHitRefParseFormat -fuzztime="$FUZZTIME"
  run go test ./internal/archive -run=^$ -fuzz=FuzzWalkBundleRoundTrip -fuzztime="$FUZZTIME"
  run go test ./internal/adapters -run=^$ -fuzz=FuzzParseGenericJSONL -fuzztime="$FUZZTIME"
  run go test ./internal/depot -run=^$ -fuzz=FuzzParseAddress -fuzztime="$FUZZTIME"
  run go test ./internal/depot -run=^$ -fuzz=FuzzValidateBundleKey -fuzztime="$FUZZTIME"
}

full() {
  quick
  run go vet ./...
  run go test -race ./...
  fuzz
  run go build -o /tmp/aha ./cmd/aha
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
  race) run go test -race ./... ;;
  build) run go build -o /tmp/aha ./cmd/aha ;;
  mutation-dry) mutation_dry ;;
  mutation) mutation ;;
  *) usage >&2; exit 2 ;;
esac
