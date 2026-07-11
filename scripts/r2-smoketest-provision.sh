#!/usr/bin/env bash
# Provision the immutable identity attestation for aha's one pinned R2 smoke
# target. This intentionally accepts no bucket/account arguments: changing the
# target requires a reviewed source change, not a runtime flag.
set -euo pipefail

cd "$(dirname "$0")/.."

if [ "$#" -ne 0 ]; then
  echo "error: the R2 smoketest target is source-pinned; this command accepts no arguments" >&2
  echo "next: run scripts/r2-smoketest-provision.sh without arguments" >&2
  exit 2
fi
if ! command -v npx >/dev/null 2>&1; then
  echo "error: npx/wrangler is required to provision the pinned test target" >&2
  echo "next: install Wrangler, authenticate the owning account, then rerun scripts/r2-smoketest-provision.sh" >&2
  exit 2
fi

bucket="aha-depot-test-ebb92642-3301-4021-84b7-31ae4c34e7cd"
key="aha-r2-smoketest-target-v1.json"
attestation="scripts/r2-smoketest-target.json"

npx --yes wrangler r2 object put "$bucket/$key" \
  --file "$attestation" \
  --content-type application/json \
  --remote

echo "provisioned pinned R2 smoketest attestation: $bucket/$key"
