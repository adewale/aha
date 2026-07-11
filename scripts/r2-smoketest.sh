#!/usr/bin/env bash
#
# aha R2 live-bucket smoketest — runs depot v2 integration tests against one
# explicitly selected, dedicated test bucket.
#
# Usage (the project test bucket/account are pinned defaults):
#   scripts/r2-smoketest.sh
#
# Override only for another dedicated test target:
#   scripts/r2-smoketest.sh --bucket NAME --account-id ACCOUNT_ID
#
# Missing credentials are prompted for securely on an interactive terminal.
# They are intentionally not accepted as command-line flags because argv and
# shell history are not secret stores.
#
# Child-test logs are never streamed because dependencies could echo secrets.
# Pass --verbose (or set AHA_R2_SMOKETEST_VERBOSE=1) to retain the private 0600
# diagnostic log and print its path after the run.
#
# Isolation contract:
#   * only AHA_R2_SMOKETEST_* target/credential variables are consumed;
#   * production AHA_R2_*, R2_*, and AWS_* credentials are never fallbacks and
#     are removed from the child environment;
#   * test credentials matching an ambient production key are rejected;
#   * every object is run-namespaced and removed afterward; production depot
#     code itself still has no delete primitive.
#
set -u -o pipefail

cd "$(dirname "$0")/.." || exit 2

usage() {
  cat >&2 <<'EOF'
usage: scripts/r2-smoketest.sh [--bucket NAME] [--account-id ID | --endpoint URL] [--region auto] [--verbose]

Default target:
  bucket:  aha-depot-test-ebb92642-3301-4021-84b7-31ae4c34e7cd
  account: 8837d43caf5a2ab3df5143eb3e2f1b96

Credentials must use the dedicated variables below or be entered at the TTY:
  AHA_R2_SMOKETEST_ACCESS_KEY_ID
  AHA_R2_SMOKETEST_SECRET_ACCESS_KEY

Production AHA_R2_*, R2_*, and AWS_* credentials are never fallback inputs.
EOF
}

DEFAULT_SMOKE_BUCKET="aha-depot-test-ebb92642-3301-4021-84b7-31ae4c34e7cd"
DEFAULT_SMOKE_ACCOUNT_ID="8837d43caf5a2ab3df5143eb3e2f1b96"
SMOKE_BUCKET="${AHA_R2_SMOKETEST_BUCKET:-$DEFAULT_SMOKE_BUCKET}"
SMOKE_ACCOUNT_ID="${AHA_R2_SMOKETEST_ACCOUNT_ID:-$DEFAULT_SMOKE_ACCOUNT_ID}"
SMOKE_ENDPOINT="${AHA_R2_SMOKETEST_ENDPOINT:-}"
SMOKE_REGION="${AHA_R2_SMOKETEST_REGION:-auto}"
SMOKE_ACCESS_KEY_ID="${AHA_R2_SMOKETEST_ACCESS_KEY_ID:-}"
SMOKE_SECRET_ACCESS_KEY="${AHA_R2_SMOKETEST_SECRET_ACCESS_KEY:-}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --bucket)
      [ "$#" -ge 2 ] || { echo "error: --bucket requires a value" >&2; usage; echo "next: rerun with an explicit dedicated test bucket" >&2; exit 2; }
      SMOKE_BUCKET="$2"; shift 2 ;;
    --account-id)
      [ "$#" -ge 2 ] || { echo "error: --account-id requires a value" >&2; usage; echo "next: rerun with the account that owns the dedicated test bucket" >&2; exit 2; }
      SMOKE_ACCOUNT_ID="$2"; shift 2 ;;
    --endpoint)
      [ "$#" -ge 2 ] || { echo "error: --endpoint requires a value" >&2; usage; echo "next: rerun with the dedicated test service endpoint" >&2; exit 2; }
      SMOKE_ENDPOINT="$2"; shift 2 ;;
    --region)
      [ "$#" -ge 2 ] || { echo "error: --region requires a value" >&2; usage; echo "next: rerun with --region auto for Cloudflare R2" >&2; exit 2; }
      SMOKE_REGION="$2"; shift 2 ;;
    --verbose)
      export AHA_R2_SMOKETEST_VERBOSE=1; shift ;;
    -h|--help)
      usage; exit 0 ;;
    *)
      echo "error: unknown smoketest option: $1" >&2
      usage
      echo "next: rerun using only the documented test-target options" >&2
      exit 2 ;;
  esac
done

if [ -z "$SMOKE_ACCESS_KEY_ID" ] && [ -t 0 ]; then
  printf 'R2 smoketest access key ID: ' >&2
  IFS= read -r SMOKE_ACCESS_KEY_ID
fi
if [ -z "$SMOKE_SECRET_ACCESS_KEY" ] && [ -t 0 ]; then
  printf 'R2 smoketest secret access key: ' >&2
  IFS= read -r -s SMOKE_SECRET_ACCESS_KEY
  printf '\n' >&2
fi

missing=0
[ -n "$SMOKE_BUCKET" ] || { echo "  missing: --bucket (or AHA_R2_SMOKETEST_BUCKET)" >&2; missing=1; }
[ -n "$SMOKE_ACCOUNT_ID$SMOKE_ENDPOINT" ] || { echo "  missing: --account-id or --endpoint" >&2; missing=1; }
[ -n "$SMOKE_ACCESS_KEY_ID" ] || { echo "  missing: AHA_R2_SMOKETEST_ACCESS_KEY_ID (or interactive input)" >&2; missing=1; }
[ -n "$SMOKE_SECRET_ACCESS_KEY" ] || { echo "  missing: AHA_R2_SMOKETEST_SECRET_ACCESS_KEY (or interactive input)" >&2; missing=1; }
if [ "$missing" -ne 0 ]; then
  echo "error: the smoketest requires one complete, explicitly test-scoped target capability; no network request was made" >&2
  echo "next: create an Object Read & Write S3 token scoped only to the pinned test bucket, then rerun and enter its test credentials (or set AHA_R2_SMOKETEST_* in CI)" >&2
  exit 2
fi

# Account IDs/endpoints may legitimately match production. The bucket-scoped
# key capability must be distinct.
for production_name in AHA_R2_ACCESS_KEY_ID R2_ACCESS_KEY_ID AWS_ACCESS_KEY_ID; do
  eval "production_value=\${$production_name:-}"
  if [ -n "$production_value" ] && [ "$production_value" = "$SMOKE_ACCESS_KEY_ID" ]; then
    echo "error: smoketest access key matches ambient production credentials; no network request was made" >&2
    echo "next: create a distinct test-scoped R2 S3 token for '$SMOKE_BUCKET' and provide it through AHA_R2_SMOKETEST_ACCESS_KEY_ID/AHA_R2_SMOKETEST_SECRET_ACCESS_KEY" >&2
    exit 2
  fi
done
for production_name in AHA_R2_SECRET_ACCESS_KEY R2_SECRET_ACCESS_KEY AWS_SECRET_ACCESS_KEY; do
  eval "production_value=\${$production_name:-}"
  if [ -n "$production_value" ] && [ "$production_value" = "$SMOKE_SECRET_ACCESS_KEY" ]; then
    echo "error: smoketest secret matches ambient production credentials; no network request was made" >&2
    echo "next: create a distinct test-scoped R2 S3 token for '$SMOKE_BUCKET' and provide it through AHA_R2_SMOKETEST_ACCESS_KEY_ID/AHA_R2_SMOKETEST_SECRET_ACCESS_KEY" >&2
    exit 2
  fi
done

export AHA_R2_SMOKETEST_BUCKET="$SMOKE_BUCKET"
export AHA_R2_SMOKETEST_ACCOUNT_ID="$SMOKE_ACCOUNT_ID"
export AHA_R2_SMOKETEST_ENDPOINT="$SMOKE_ENDPOINT"
export AHA_R2_SMOKETEST_REGION="$SMOKE_REGION"
export AHA_R2_SMOKETEST_ACCESS_KEY_ID="$SMOKE_ACCESS_KEY_ID"
export AHA_R2_SMOKETEST_SECRET_ACCESS_KEY="$SMOKE_SECRET_ACCESS_KEY"

# Defense in depth: even a future accidental default-provider lookup in the
# integration process cannot see production credential names.
unset AHA_R2_ACCOUNT_ID AHA_R2_ENDPOINT AHA_R2_REGION AHA_R2_ACCESS_KEY_ID AHA_R2_SECRET_ACCESS_KEY
unset R2_ACCOUNT_ID R2_ENDPOINT R2_REGION R2_ACCESS_KEY_ID R2_SECRET_ACCESS_KEY
unset AWS_ACCESS_KEY_ID AWS_SECRET_ACCESS_KEY AWS_SESSION_TOKEN AWS_ENDPOINT_URL AWS_ENDPOINT_URL_S3
unset AWS_PROFILE AWS_DEFAULT_PROFILE AWS_SHARED_CREDENTIALS_FILE AWS_CONFIG_FILE AWS_REGION AWS_DEFAULT_REGION
unset AWS_WEB_IDENTITY_TOKEN_FILE AWS_ROLE_ARN AWS_ROLE_SESSION_NAME
unset AWS_CONTAINER_CREDENTIALS_FULL_URI AWS_CONTAINER_CREDENTIALS_RELATIVE_URI
unset AWS_CONTAINER_AUTHORIZATION_TOKEN AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE

if ! command -v go >/dev/null 2>&1; then
  echo "error: go is not on PATH; the smoketest runs the live integration suite with Go" >&2
  echo "next: install Go or add its bin directory to PATH, then rerun scripts/r2-smoketest.sh" >&2
  exit 2
fi

echo "progress phase=preflight state=completed" >&2
echo "R2 smoketest: bucket=$SMOKE_BUCKET" >&2
echo "using an explicit test-only credential capability; production credential names are absent from the child" >&2
echo "objects are namespaced per run and deleted afterwards; see header for details" >&2
echo "the suite includes concurrent first pushes and can take longer than the single-writer check" >&2
echo >&2

# -count=1 forces a real run: a cached pass would vouch for nothing.
echo "progress phase=integration_test state=started" >&2
smoke_log=$(mktemp "${TMPDIR:-/tmp}/aha-r2-smoketest.XXXXXX") || {
  echo "progress phase=integration_test state=failed" >&2
  echo "R2 smoketest FAILED: cannot create a private diagnostic log" >&2
  echo "next: check temporary-directory permissions and rerun scripts/r2-smoketest.sh" >&2
  exit 2
}
chmod 600 "$smoke_log" || {
  rm -f "$smoke_log"
  echo "progress phase=integration_test state=failed" >&2
  echo "R2 smoketest FAILED: cannot make the diagnostic log private" >&2
  echo "next: check temporary-directory permissions and rerun scripts/r2-smoketest.sh" >&2
  exit 2
}
keep_log="${AHA_R2_SMOKETEST_VERBOSE:-0}"
if [ "$keep_log" = 1 ]; then
  trap 'echo "diagnostic log retained at: $smoke_log" >&2' EXIT
  trap 'exit 130' HUP INT TERM
else
  trap 'rm -f "$smoke_log"' EXIT HUP INT TERM
fi
# Child output is never copied to stdout/stderr: dependencies must not be able
# to echo credentials into normal operator output. --verbose retains the
# private 0600 log for explicit local inspection instead.
go test -tags integration -count=1 -run 'TestR2IntegrationV2' ./internal/depot/ -v >"$smoke_log" 2>&1
status=$?
if [ "$status" -eq 0 ]; then
  echo >&2
  echo "progress phase=integration_test state=completed" >&2
  echo "R2 smoketest PASSED against bucket '$SMOKE_BUCKET'" >&2
else
  echo >&2
  echo "progress phase=integration_test state=failed" >&2
  echo "R2 smoketest FAILED (exit $status)" >&2
  if grep -Eiq 'authorization denied during HeadBucket and ListObjectsV2' "$smoke_log"; then
    echo "R2 authorization denied during both HeadBucket and the bounded ListObjectsV2 fallback, before any smoke objects were written." >&2
    echo "The explicit test credential does not authorize this test bucket/account endpoint." >&2
    echo "next: create a matching Object Read & Write R2 S3 token scoped only to '$SMOKE_BUCKET', then rerun with the AHA_R2_SMOKETEST_* variables" >&2
  elif grep -Eiq 'HeadBucket.*(StatusCode: 403|Forbidden)|StatusCode: 403.*HeadBucket|authorization denied during HeadBucket' "$smoke_log"; then
    echo "R2 authorization denied during HeadBucket, before any smoke objects were written." >&2
    echo "The explicit test credential does not authorize this test bucket/account endpoint." >&2
    echo "next: create a matching Object Read & Write R2 S3 token scoped only to '$SMOKE_BUCKET', then rerun with the AHA_R2_SMOKETEST_* variables" >&2
  else
    echo "The integration test failed; raw child diagnostics are hidden by default." >&2
    echo "next: rerun scripts/r2-smoketest.sh with the same explicit target and --verbose" >&2
  fi
  exit "$status"
fi
