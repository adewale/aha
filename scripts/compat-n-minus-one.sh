#!/usr/bin/env bash
set -euo pipefail

root=$(cd "$(dirname "$0")/.." && pwd)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/aha-compat.XXXXXX")
worktree="$tmp/n-minus-one"
# Pinned predecessor of this audit remediation, never a moving branch ref.
n_minus_one=b550e6bd031e8dc89f18b03348f1053f74d7d603
cleanup() {
  git -C "$root" worktree remove --force "$worktree" >/dev/null 2>&1 || true
  rm -rf "$tmp"
}
trap cleanup EXIT

git -C "$root" worktree add --detach "$worktree" "$n_minus_one" >/dev/null
go build -C "$worktree" -o "$tmp/aha-n-minus-one" ./cmd/aha
go build -C "$root" -o "$tmp/aha-current" ./cmd/aha

mkdir -p "$tmp/pi/--Users-compat-proj--"
cat >"$tmp/pi/--Users-compat-proj--/session.jsonl" <<'EOF'
{"type":"session","version":3,"id":"compat-session","timestamp":"2026-01-01T00:00:00Z","cwd":"/compat/proj"}
{"id":"p1","parentId":"","type":"user","role":"user","timestamp":"2026-01-01T00:00:01Z","message":{"content":"n-minus-one compatibility needle"}}
{"id":"p2","parentId":"p1","type":"assistant","role":"assistant","timestamp":"2026-01-01T00:00:02Z","message":{"content":[{"type":"text","text":"compatible reply"}]}}
EOF

legacy_archive="$tmp/legacy-archive"
workspace="$tmp/workspace"
config="$tmp/config.jsonc"
cat >"$config" <<EOF
{
  "schema":"aha.config.v1",
  "machine_id":"compat-machine",
  "sources":[{"type":"pi","root":"$tmp/pi","enabled":true}],
  "workspace_dir":"$workspace",
  "archive":{"type":"local","location":"$legacy_archive"},
  "path_mode":"raw",
  "include_subagents":true,
  "include_images":true,
  "redaction":"none-v1",
  "acknowledged_raw_history":true
}
EOF

# N-1 writes a real populated v2 Archive; N reads, verifies, and materialises it.
"$tmp/aha-n-minus-one" archive init --config "$config" >/dev/null
"$tmp/aha-n-minus-one" archive upload --config "$config" >/dev/null
cp -R "$legacy_archive" "$tmp/legacy-before"
"$tmp/aha-current" archive status --config "$config" --json >/dev/null
"$tmp/aha-current" archive verify --config "$config" --deep --json >/dev/null
"$tmp/aha-current" archive download --config "$config" --json >/dev/null
search_output=$("$tmp/aha-current" search "compatibility needle" --config "$config" --json)
grep -q '"ref_text"' <<<"$search_output"
diff -ru "$tmp/legacy-before" "$legacy_archive" >/dev/null

# Reverse direction: the pinned writer refuses the current v3 generation and
# leaves it byte-for-byte unchanged.
current_archive="$tmp/current-archive"
"$tmp/aha-current" archive init "local:$current_archive" --config "$config" >/dev/null
cp -R "$current_archive" "$tmp/current-before"
if "$tmp/aha-n-minus-one" archive upload "local:$current_archive" --config "$config" --json >/dev/null 2>&1; then
  echo "N-1 binary accepted the current writer-fenced Archive" >&2
  exit 1
fi
diff -ru "$tmp/current-before" "$current_archive" >/dev/null

echo "N/N-1 Archive compatibility: pass"
