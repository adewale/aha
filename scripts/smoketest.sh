#!/usr/bin/env bash
#
# aha source-adapter smoketest — SAFE / READ-ONLY against a real machine.
#
# Usage:
#   scripts/smoketest.sh <opencode|codex|claude|pi> [SOURCE_ROOT]
#
# What it does (and does NOT do):
#   * Reads your real agent history for the chosen source.
#   * Writes EVERYTHING it generates under a single /tmp directory — a throwaway
#     corpus, depot, config, cache, and the OpenCode JSONL export. Your real
#     ~/.aha, ~/.config/aha, and shell caches are never touched.
#   * Never writes to the source data. It records a fingerprint of the source
#     tree (and, for OpenCode, a content hash + integrity check of the .db)
#     before and after the run and fails if anything changed.
#   * Because all artifacts live in /tmp, there is nothing to clean up; /tmp is
#     reclaimed by the OS. (You may `rm -rf` the printed directory if you like.)
#
set -u -o pipefail

# ---- arguments -------------------------------------------------------------

SOURCE="${1:-}"
SOURCE_ROOT_ARG="${2:-}"
case "$SOURCE" in
  opencode|codex|claude|pi) ;;
  *)
    echo "usage: $0 <opencode|codex|claude|pi> [SOURCE_ROOT]" >&2
    exit 2
    ;;
esac

# aha's internal source type name (claude -> claude-code).
SOURCE_TYPE="$SOURCE"
[ "$SOURCE" = "claude" ] && SOURCE_TYPE="claude-code"

# ---- portable helpers ------------------------------------------------------

if command -v sha256sum >/dev/null 2>&1; then
  sha256_stdin() { sha256sum | awk '{print $1}'; }
  sha256_file()  { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256_stdin() { shasum -a 256 | awk '{print $1}'; }
  sha256_file()  { shasum -a 256 "$1" | awk '{print $1}'; }
else
  echo "error: need sha256sum or shasum on PATH" >&2
  exit 2
fi

case "$(uname)" in
  Darwin) STAT_FMT='-f %N|%z|%m' ;;
  *)      STAT_FMT='-c %n|%s|%Y' ;;
esac

# Fingerprint a tree by name+size+mtime only (no content read — fast, and still
# detects any create/modify/delete). Returns a single hash, or "absent".
fingerprint_tree() {
  local root="$1"
  [ -e "$root" ] || { echo "absent"; return; }
  # shellcheck disable=SC2086
  find "$root" -type f 2>/dev/null | LC_ALL=C sort | while IFS= read -r f; do
    stat $STAT_FMT "$f" 2>/dev/null
  done | sha256_stdin
}

PASS=0 FAIL=0
ok()   { printf '  \033[32mPASS\033[0m %s\n' "$1"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; FAIL=$((FAIL+1)); }
note() { printf '  ---- %s\n' "$1"; }

# ---- resolve the source root ----------------------------------------------

default_root() {
  case "$SOURCE" in
    opencode)
      if [ -n "${OPENCODE_DB:-}" ]; then echo "$OPENCODE_DB"
      else echo "${XDG_DATA_HOME:-$HOME/.local/share}/opencode"; fi ;;
    codex)  echo "$HOME/.codex/sessions" ;;
    claude) echo "$HOME/.claude/projects" ;;
    pi)     echo "$HOME/.pi/agent/sessions" ;;
  esac
}
SOURCE_ROOT="${SOURCE_ROOT_ARG:-$(default_root)}"

if [ ! -e "$SOURCE_ROOT" ]; then
  echo "error: source root not found: $SOURCE_ROOT" >&2
  echo "       pass an explicit path: $0 $SOURCE /path/to/source" >&2
  exit 2
fi

# ---- /tmp workspace (everything lives here) --------------------------------

WORK="$(mktemp -d "/tmp/aha-smoketest-${SOURCE}.XXXXXX")"
CORPUS="$WORK/corpus"
DEPOT="$WORK/depot"
CFG="$WORK/config.jsonc"
LOGS="$WORK/logs"
mkdir -p "$CORPUS" "$DEPOT" "$LOGS" "$WORK/cache" "$WORK/tmp" "$WORK/export"

# Corral every cache/temp write into /tmp, cross-platform.
export XDG_CACHE_HOME="$WORK/cache"
export XDG_CONFIG_HOME="$WORK/cache/config"
export TMPDIR="$WORK/tmp"
export AHA_OPENCODE_EXPORT_DIR="$WORK/export"   # honored by the opencode adapter

echo "aha smoketest: source=$SOURCE  root=$SOURCE_ROOT"
echo "all artifacts under: $WORK"
echo

# ---- locate / build the aha binary ----------------------------------------

AHA="${AHA_BIN:-}"
if [ -z "$AHA" ]; then
  if command -v aha >/dev/null 2>&1; then
    AHA="$(command -v aha)"
  elif [ -d "./cmd/aha" ] && command -v go >/dev/null 2>&1; then
    echo "building aha into $WORK/aha ..."
    if go build -o "$WORK/aha" ./cmd/aha; then AHA="$WORK/aha"; fi
  fi
fi
if [ -z "$AHA" ] || [ ! -x "$AHA" ]; then
  echo "error: could not find or build 'aha' (set AHA_BIN, or run from the repo with go installed)" >&2
  exit 2
fi
note "aha binary: $AHA"

# ---- throwaway config pointing only at /tmp + the one source ---------------

cat > "$CFG" <<JSON
{
  "machine_id": "smoketest",
  "sources": [
    { "type": "$SOURCE_TYPE", "root": "$SOURCE_ROOT", "enabled": true }
  ],
  "corpus_dir": "$CORPUS",
  "depot": { "type": "local", "location": "$DEPOT" },
  "include_subagents": true,
  "include_images": true,
  "index_tool_output": false,
  "redaction": "none-v1",
  "accept_secrets_warning": true
}
JSON

# aha parses the first non-flag token as the command, so every flag must come
# AFTER the subcommand.
aha_cfg()  { "$AHA" "$@" --config "$CFG"; }                  # commands that read config
aha_repo() { "$AHA" "$@" --repo "$CORPUS" --config "$CFG"; } # read-only corpus queries

# ---- BEFORE: source fingerprint (read-only guarantee) ----------------------

echo "== capturing source fingerprint (before) =="
SRC_FP_BEFORE="$(fingerprint_tree "$SOURCE_ROOT")"
note "tree fingerprint: $SRC_FP_BEFORE"

# OpenCode: also content-hash the database file(s) and integrity-check them.
OC_DBS=()
if [ "$SOURCE" = "opencode" ]; then
  if [ -f "$SOURCE_ROOT" ]; then OC_DBS+=("$SOURCE_ROOT")
  else
    [ -f "$SOURCE_ROOT/opencode.db" ] && OC_DBS+=("$SOURCE_ROOT/opencode.db")
    while IFS= read -r d; do [ -n "$d" ] && OC_DBS+=("$d"); done \
      < <(find "$SOURCE_ROOT" -maxdepth 1 -name 'opencode-*.db' 2>/dev/null | LC_ALL=C sort)
  fi
  if [ "${#OC_DBS[@]}" -eq 0 ]; then
    bad "no opencode.db found under $SOURCE_ROOT"
  fi
  declare -A OC_HASH_BEFORE=()
  for d in "${OC_DBS[@]}"; do
    OC_HASH_BEFORE["$d"]="$(sha256_file "$d")"
    note "db $d: ${OC_HASH_BEFORE["$d"]}"
  done
fi
echo

# ---- discovery: doctor -----------------------------------------------------

echo "== aha doctor (discovery) =="
aha_cfg doctor --json > "$LOGS/doctor.json" 2>"$LOGS/doctor.err"
if [ $? -eq 0 ]; then ok "doctor exited cleanly"; else bad "doctor failed (see $LOGS/doctor.err)"; fi
if command -v jq >/dev/null 2>&1; then
  SESS_COUNT="$(jq "[.sources[]|select(.type==\"$SOURCE_TYPE\")|.session_files]|add // 0" "$LOGS/doctor.json" 2>/dev/null)"
  SRC_OK="$(jq -r "[.sources[]|select(.type==\"$SOURCE_TYPE\")|.ok]|all" "$LOGS/doctor.json" 2>/dev/null)"
else
  # space-tolerant fallback for pretty-printed JSON
  SESS_COUNT="$(grep -oE '"session_files":[[:space:]]*[0-9]+' "$LOGS/doctor.json" | grep -oE '[0-9]+' | head -1)"
  if grep -qE '"ok":[[:space:]]*false' "$LOGS/doctor.json"; then SRC_OK="false"; else SRC_OK="true"; fi
fi
SESS_COUNT="${SESS_COUNT:-0}"
if [ "$SRC_OK" = "true" ]; then ok "doctor reports source ok"; else bad "doctor did not report ok (see $LOGS/doctor.json)"; fi
note "discovered session files: $SESS_COUNT"
echo

# ---- snapshot + ingest into the /tmp corpus --------------------------------

echo "== aha refresh (snapshot -> /tmp depot -> /tmp corpus) =="
aha_cfg refresh --json > "$LOGS/refresh.json" 2>"$LOGS/refresh.err"
if [ $? -eq 0 ]; then ok "refresh exited cleanly"; else bad "refresh failed (see $LOGS/refresh.err)"; fi
echo

echo "== aha status / verify (corpus health) =="
aha_repo status --json > "$LOGS/status.json" 2>"$LOGS/status.err" \
  && ok "status exited cleanly" || bad "status failed (see $LOGS/status.err)"
aha_repo verify --json > "$LOGS/verify.json" 2>"$LOGS/verify.err" \
  && ok "verify exited cleanly" || bad "verify failed (see $LOGS/verify.err)"
echo

# ---- retrieval: search -> read --------------------------------------------

echo "== aha search -> read (end-to-end retrieval) =="
REF=""
for word in the and to of a error file function test code session message user; do
  REF="$("$AHA" search "$word" --repo "$CORPUS" --source "$SOURCE_TYPE" --refs 2>/dev/null | awk 'NF{print $1; exit}')"
  [ -n "$REF" ] && { note "matched on \"$word\" -> $REF"; break; }
done
if [ -n "$REF" ]; then
  ok "search returned at least one ref"
  if "$AHA" read "$REF" --repo "$CORPUS" --md > "$LOGS/read.md" 2>"$LOGS/read.err"; then
    ok "read returned full context ($(wc -l <"$LOGS/read.md" | tr -d ' ') lines -> $LOGS/read.md)"
  else
    bad "read failed for $REF (see $LOGS/read.err)"
  fi
elif [ "$SESS_COUNT" -eq 0 ]; then
  note "no sessions discovered — skipping retrieval (not a failure)"
else
  note "search returned no hits across common tokens — retrieval inconclusive"
fi
echo

# ---- OpenCode extras: ground-truth schema + export location ----------------

if [ "$SOURCE" = "opencode" ]; then
  echo "== opencode schema / export checks =="
  if command -v sqlite3 >/dev/null 2>&1 && [ "${#OC_DBS[@]}" -gt 0 ]; then
    # Open a private read-only copy so we never touch the live DB.
    cp "${OC_DBS[0]}" "$WORK/tmp/inspect.db"
    {
      echo "# tables"; sqlite3 "$WORK/tmp/inspect.db" ".tables"
      for t in session message part; do
        echo "# schema $t"; sqlite3 "$WORK/tmp/inspect.db" ".schema $t"
        echo "# count $t"; sqlite3 "$WORK/tmp/inspect.db" "select count(*) from $t;" 2>/dev/null
      done
    } > "$LOGS/opencode-schema.txt" 2>&1
    for t in session message part; do
      if grep -qi "CREATE TABLE \"\?$t\"\?" "$LOGS/opencode-schema.txt"; then
        ok "schema has expected table: $t"
      else
        bad "schema MISSING expected table: $t (adapter assumptions may need updating — see $LOGS/opencode-schema.txt)"
      fi
    done
    note "schema dump saved: $LOGS/opencode-schema.txt"
  else
    note "sqlite3 not on PATH — skipping schema dump (install sqlite3 for the schema check)"
  fi
  # Generated JSONL export must live under /tmp.
  if find "$WORK/export" -name '*.jsonl' 2>/dev/null | grep -q .; then
    ok "opencode JSONL export written under /tmp ($WORK/export)"
  else
    note "no JSONL export found (expected only if sessions were discovered)"
  fi
  echo
fi

# ---- AFTER: re-fingerprint source and assert it is unchanged ---------------

echo "== verifying source was NOT modified (read-only guarantee) =="
SRC_FP_AFTER="$(fingerprint_tree "$SOURCE_ROOT")"
if [ "$SRC_FP_BEFORE" = "$SRC_FP_AFTER" ]; then
  ok "source tree unchanged (name/size/mtime fingerprint identical)"
else
  bad "source tree CHANGED during run — investigate before trusting the adapter"
fi

if [ "$SOURCE" = "opencode" ] && [ "${#OC_DBS[@]}" -gt 0 ]; then
  changed=0
  for d in "${OC_DBS[@]}"; do
    after="$(sha256_file "$d")"
    [ "$after" = "${OC_HASH_BEFORE["$d"]}" ] || { changed=1; bad "database content changed: $d"; }
  done
  if [ "$changed" -eq 0 ]; then
    ok "opencode database content unchanged (sha256 identical)"
    note "if this ever fails while OpenCode is running, re-run with OpenCode closed"
  fi
  if command -v sqlite3 >/dev/null 2>&1; then
    for d in "${OC_DBS[@]}"; do
      res="$(sqlite3 "file:$d?mode=ro" 'pragma integrity_check;' 2>/dev/null | head -1)"
      [ "$res" = "ok" ] && ok "integrity_check ok: $(basename "$d")" || bad "integrity_check NOT ok: $d ($res)"
    done
  fi
fi
echo

# ---- summary ---------------------------------------------------------------

echo "============================================================"
echo "smoketest summary for '$SOURCE': $PASS passed, $FAIL failed"
echo "artifacts (safe to delete; in /tmp): $WORK"
echo "============================================================"
[ "$FAIL" -eq 0 ]
