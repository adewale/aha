#!/usr/bin/env bash
#
# aha R2 live-bucket smoketest — runs the depot v2 integration test against a
# real Cloudflare R2 (or any S3-compatible) bucket.
#
# Usage:
#   AHA_R2_TEST_BUCKET=aha-depot-test \
#   AHA_R2_ACCOUNT_ID=... AHA_R2_ACCESS_KEY_ID=... AHA_R2_SECRET_ACCESS_KEY=... \
#   scripts/r2-smoketest.sh
#
# What it proves that the in-repo fake-S3 suite cannot:
#   * real conditional writes (If-Match / If-None-Match on the latest pointer
#     and machines index) against the live service;
#   * real strong-consistency read-after-write;
#   * shared-index convergence under simultaneous first pushes;
#   * that the AWS SDK's request/response checksum configuration is accepted
#     by R2's partial x-amz-checksum-* implementation.
#
# Bucket safety: the test namespaces every object under a unique machine ID
# (smoke-<nanoseconds>) with run-unique content, asserts push/pull/verify, and
# deletes its own objects afterward with raw SDK calls (production depot code
# cannot delete). A pre-existing depot in the bucket is read but never
# modified. A dedicated test bucket is still the recommendation: `verify` in a
# shared bucket reads other machines' manifests, and an interrupted run can
# leave uniquely-named smoke objects behind.
#
set -u -o pipefail

cd "$(dirname "$0")/.." || exit 2

# ---- preflight: every missing variable reported at once ---------------------

missing=0
need() {
  # need VAR [ALT]: VAR (or its ALT alias) must be set and non-empty.
  local primary="$1" alt="${2:-}" val=""
  eval "val=\${$primary:-}"
  if [ -z "$val" ] && [ -n "$alt" ]; then
    eval "val=\${$alt:-}"
  fi
  if [ -z "$val" ]; then
    if [ -n "$alt" ]; then
      echo "  missing: $primary (or $alt)" >&2
    else
      echo "  missing: $primary" >&2
    fi
    missing=1
  fi
}

need AHA_R2_TEST_BUCKET
need AHA_R2_ACCESS_KEY_ID R2_ACCESS_KEY_ID
need AHA_R2_SECRET_ACCESS_KEY R2_SECRET_ACCESS_KEY

# The endpoint comes from the account ID unless set explicitly
# (jurisdiction buckets, MinIO, fake-S3).
if [ -z "${AHA_R2_ACCOUNT_ID:-}${R2_ACCOUNT_ID:-}${AHA_R2_ENDPOINT:-}${R2_ENDPOINT:-}" ]; then
  echo "  missing: AHA_R2_ACCOUNT_ID (or R2_ACCOUNT_ID, or an explicit AHA_R2_ENDPOINT)" >&2
  missing=1
fi

if [ "$missing" -ne 0 ]; then
  cat >&2 <<'EOF'

R2 smoketest needs S3 credentials for a test bucket. Setup, in short:
  1. Create a private test bucket:  npx wrangler r2 bucket create aha-depot-test
  2. Create an R2 API token (dashboard: R2 Object Storage -> API tokens) with
     Object Read & Write scoped to that bucket; copy the Access Key ID and
     Secret Access Key when shown — the secret appears only once.
  3. Find the account ID:  npx wrangler whoami   (or the dash.cloudflare.com URL)

Details: docs/r2-bucket-settings.md and docs/onboarding.md (section 8).
EOF
  exit 2
fi

if ! command -v go >/dev/null 2>&1; then
  echo "error: go is not on PATH; the smoketest runs 'go test -tags integration'" >&2
  exit 2
fi

echo "progress phase=preflight state=completed" >&2
echo "R2 smoketest: bucket=$AHA_R2_TEST_BUCKET" >&2
echo "objects are namespaced per run and deleted afterwards; see header for details" >&2
echo "the suite includes concurrent first pushes and can take longer than the single-writer check" >&2
echo >&2

# ---- run ---------------------------------------------------------------------

# -count=1 forces a real run: a cached "pass" would vouch for nothing about
# the live service. The test itself skips (exit 0 with skip notice) only if
# credentials resolve but are empty — the preflight above makes that loud.
echo "progress phase=integration_test state=started" >&2
if go test -tags integration -count=1 -run 'TestR2IntegrationV2' ./internal/depot/ -v; then
  echo >&2
  echo "progress phase=integration_test state=completed" >&2
  echo "R2 smoketest PASSED against bucket '$AHA_R2_TEST_BUCKET'" >&2
else
  status=$?
  echo >&2
  echo "progress phase=integration_test state=failed" >&2
  echo "R2 smoketest FAILED (exit $status)" >&2
  echo "diagnose credentials/endpoint problems with: aha doctor --depot r2:$AHA_R2_TEST_BUCKET --json" >&2
  exit "$status"
fi
