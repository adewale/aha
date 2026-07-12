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
#     ~/.aha, ~/.config/aha, shell caches, and Go build/module caches are never touched.
#   * Never writes to the source data. It records a content fingerprint of the source
#     tree (and, for OpenCode, content hashes + integrity checks of DB files/sidecars)
#     before and after the run and fails if anything changed.
#   * Because all artifacts live in /tmp, there is nothing to clean up; /tmp is
#     reclaimed by the OS. (You may `rm -rf` the printed directory if you like.)
#
# Compatibility: portable to macOS's default bash 3.2 (no associative arrays)
# and BSD/GNU coreutils. `sqlite3` is optional — when present it adds an
# OpenCode schema check; when absent that single check is skipped.
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

# Fingerprint a file/tree by path metadata plus file content. Returns a single
# hash, or "absent". This is intentionally stronger than a size/mtime smoke
# signal because the script is a read-only regression check.
fingerprint_tree() {
  local root="$1"
  [ -e "$root" ] || { echo "absent"; return; }
  if [ -f "$root" ]; then
    { stat $STAT_FMT "$root" 2>/dev/null; sha256_file "$root"; } | sha256_stdin
    return
  fi
  # shellcheck disable=SC2086
  find "$root" -type f 2>/dev/null | LC_ALL=C sort | while IFS= read -r f; do
    stat $STAT_FMT "$f" 2>/dev/null
    sha256_file "$f"
  done | sha256_stdin
}

PASS=0 FAIL=0
ok()   { printf '  \033[32mPASS\033[0m %s\n' "$1"; PASS=$((PASS+1)); }
bad()  { printf '  \033[31mFAIL\033[0m %s\n' "$1"; FAIL=$((FAIL+1)); }
note() { printf '  ---- %s\n' "$1"; }

# json_num FILE KEY -> first numeric value for that top-level key (jq if present,
# else a space-tolerant grep). Prints 0 when absent.
json_num() {
  local out
  if command -v jq >/dev/null 2>&1; then
    out="$(jq -r "(.$2 // 0)" "$1" 2>/dev/null)"
  else
    out="$(grep -oE "\"$2\"[[:space:]]*:[[:space:]]*[0-9]+" "$1" 2>/dev/null | grep -oE '[0-9]+' | head -1)"
  fi
  printf '%s' "${out:-0}"
}

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
if [ "$SOURCE" = "opencode" ] && [ -n "$SOURCE_ROOT_ARG" ]; then
  # An explicit SOURCE_ROOT should be the thing under test. OPENCODE_DB is an
  # adapter-level override, so leaving a parent-shell value set would make the
  # script fingerprint one DB/root while the adapter reads another.
  unset OPENCODE_DB
fi

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
export GOCACHE="$WORK/cache/go-build"
export GOMODCACHE="$WORK/cache/go-mod"
export GOPATH="$WORK/cache/go-path"
export AHA_OPENCODE_EXPORT_DIR="$WORK/export"   # honored by the opencode adapter

echo "aha smoketest: source=$SOURCE  root=$SOURCE_ROOT"
echo "all artifacts under: $WORK"
echo

# ---- locate / build the aha binary ----------------------------------------

AHA="${AHA_BIN:-}"
if [ -z "$AHA" ]; then
  if [ -d "./cmd/aha" ] && command -v go >/dev/null 2>&1; then
    echo "building aha into $WORK/aha ..."
    if go build -o "$WORK/aha" ./cmd/aha; then AHA="$WORK/aha"; fi
  elif command -v aha >/dev/null 2>&1; then
    AHA="$(command -v aha)"
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
  "workspace_dir": "$CORPUS",
  "archive": { "type": "local", "location": "$DEPOT" },
  "include_subagents": true,
  "include_images": true,
  "index_tool_output": false,
  "redaction": "none-v1",
  "acknowledged_raw_history": true
}
JSON

# aha parses the first non-flag token as the command, so every flag must come
# AFTER the subcommand.
aha_cfg() { "$AHA" "$@" --config "$CFG"; }
aha_ws()  { "$AHA" "$@" --workspace "$CORPUS" --config "$CFG"; }

# ---- BEFORE: source fingerprint (read-only guarantee) ----------------------

echo "== capturing source fingerprint (before) =="
SRC_FP_BEFORE="$(fingerprint_tree "$SOURCE_ROOT")"
note "content fingerprint: $SRC_FP_BEFORE"

# OpenCode: also content-hash the database file(s), sidecars, and integrity-check DBs.
# Parallel indexed arrays (OC_HASH_PATHS[i] <-> OC_HASH_BEFORE[i]) — associative arrays
# need bash 4+, but macOS ships bash 3.2.
OC_DBS=()
OC_HASH_PATHS=()
OC_HASH_BEFORE=()
if [ "$SOURCE" = "opencode" ]; then
  if [ -f "$SOURCE_ROOT" ]; then OC_DBS+=("$SOURCE_ROOT")
  else
    [ -f "$SOURCE_ROOT/opencode.db" ] && OC_DBS+=("$SOURCE_ROOT/opencode.db")
    while IFS= read -r d; do [ -n "$d" ] && OC_DBS+=("$d"); done \
      < <(find "$SOURCE_ROOT" -maxdepth 1 -name 'opencode-*.db' 2>/dev/null | LC_ALL=C sort)
  fi
  if [ "${#OC_DBS[@]}" -eq 0 ]; then
    bad "no opencode.db found under $SOURCE_ROOT"
  else
    for d in "${OC_DBS[@]}"; do
      OC_HASH_PATHS+=("$d")
      [ -f "$d-wal" ] && OC_HASH_PATHS+=("$d-wal")
      [ -f "$d-shm" ] && OC_HASH_PATHS+=("$d-shm")
    done
    for i in "${!OC_HASH_PATHS[@]}"; do
      OC_HASH_BEFORE[$i]="$(sha256_file "${OC_HASH_PATHS[$i]}")"
      note "source ${OC_HASH_PATHS[$i]}: ${OC_HASH_BEFORE[$i]}"
    done
  fi
fi
echo

# ---- inspect, upload, and download -----------------------------------------

echo "== aha status (discovery) =="
aha_cfg status --json > "$LOGS/system-status.json" 2>"$LOGS/system-status.err" || true
if command -v jq >/dev/null 2>&1; then
  SESS_COUNT="$(jq '.agent_history.files // 0' "$LOGS/system-status.json" 2>/dev/null)"
else
  SESS_COUNT="$(grep -oE '"files":[[:space:]]*[0-9]+' "$LOGS/system-status.json" | grep -oE '[0-9]+' | head -1)"
fi
SESS_COUNT="${SESS_COUNT:-0}"
note "discovered history files: $SESS_COUNT"

echo "== aha archive init -> upload -> download =="
aha_cfg archive init --json > "$LOGS/archive-init.json" 2>"$LOGS/archive-init.err" \
  && ok "Archive initialised" || bad "Archive init failed"
aha_cfg archive upload --json > "$LOGS/archive-upload.json" 2>"$LOGS/archive-upload.err" \
  && ok "Archive upload exited cleanly" || bad "Archive upload failed"
aha_cfg archive download --json > "$LOGS/archive-download.json" 2>"$LOGS/archive-download.err" \
  && ok "Archive download exited cleanly" || bad "Archive download failed"
echo

echo "== aha workspace status / verify =="
aha_cfg workspace status --json > "$LOGS/status.json" 2>"$LOGS/status.err" \
  && ok "Workspace status exited cleanly" || bad "Workspace status failed (see $LOGS/status.err)"
aha_cfg workspace verify --json > "$LOGS/verify.json" 2>"$LOGS/verify.err" \
  && ok "Workspace verify exited cleanly" || bad "Workspace verify failed (see $LOGS/verify.err)"
# Ingestion depth: distinguishes "adapter parsed nothing" from "indexed fine".
NSESS="$(json_num "$LOGS/status.json" sessions)"
NMSG="$(json_num "$LOGS/status.json" messages)"
NENT="$(json_num "$LOGS/status.json" entries)"
NFTS="$(json_num "$LOGS/status.json" fts_messages)"
note "ingested: $NSESS sessions, $NENT entries, $NMSG messages, $NFTS searchable (FTS) rows"
echo

# ---- retrieval: search -> read --------------------------------------------

echo "== aha search -> read (end-to-end retrieval) =="
REF=""
SEARCH_TERMS=""
if command -v sqlite3 >/dev/null 2>&1 && [ -f "$CORPUS/corpus.db" ]; then
  SAMPLE_TEXT="$(sqlite3 "$CORPUS/corpus.db" "select m.text from messages m join sessions s on s.session_key=m.session_key where s.source_name='$SOURCE_TYPE' and trim(coalesce(m.text,''))<>'' limit 1;" 2>/dev/null)"
  SAMPLE_TERM="$(printf '%s\n' "$SAMPLE_TEXT" | tr -cs '[:alnum:]_' '\n' | awk 'length($0) >= 3 { print; exit }')"
  [ -n "$SAMPLE_TERM" ] && SEARCH_TERMS="$SAMPLE_TERM"
fi
SEARCH_TERMS="$SEARCH_TERMS the and to of a error file function test code session message user"
for word in $SEARCH_TERMS; do
  REF="$("$AHA" search "$word" --workspace "$CORPUS" --config "$CFG" --source "$SOURCE_TYPE" --refs 2>/dev/null | awk 'NF{print $1; exit}')"
  [ -n "$REF" ] && { note "matched on \"$word\" -> $REF"; break; }
done
if [ -n "$REF" ]; then
  ok "search returned at least one ref"
  if "$AHA" show "$REF" --workspace "$CORPUS" --config "$CFG" --md > "$LOGS/show.md" 2>"$LOGS/show.err"; then
    ok "show returned full context ($(wc -l <"$LOGS/show.md" | tr -d ' ') lines -> $LOGS/read.md)"
  else
    bad "show failed for $REF (see $LOGS/show.err)"
  fi
elif [ "$SESS_COUNT" -eq 0 ]; then
  note "no sessions discovered — skipping retrieval (not a failure)"
elif [ "${NMSG:-0}" -eq 0 ]; then
  bad "discovered $SESS_COUNT session file(s) but ingested 0 messages — the adapter parsed no messages from this source's real format (inspect $LOGS/archive-upload.json and a raw session file)"
elif [ "${NFTS:-0}" -eq 0 ]; then
  bad "ingested $NMSG messages but indexed 0 searchable rows — message text is not being extracted for this source's real format"
else
  bad "indexed $NFTS searchable rows but search/show returned no refs — retrieval failed (see $LOGS/status.json)"
fi
echo

# ---- OpenCode extras: ground-truth schema + export location ----------------

if [ "$SOURCE" = "opencode" ]; then
  echo "== opencode schema / export checks =="
  if command -v sqlite3 >/dev/null 2>&1 && [ "${#OC_DBS[@]}" -gt 0 ]; then
    # Open a private read-only copy so we never touch the live DB.
    cp "${OC_DBS[0]}" "$WORK/tmp/inspect.db"
    [ -f "${OC_DBS[0]}-wal" ] && cp "${OC_DBS[0]}-wal" "$WORK/tmp/inspect.db-wal"
    [ -f "${OC_DBS[0]}-shm" ] && cp "${OC_DBS[0]}-shm" "$WORK/tmp/inspect.db-shm"
    # `.tables` lists bare, unquoted names — robust to how the DDL quotes them
    # (sqlite3 may emit "session", `session`, [session], or session).
    OC_TABLES="$(sqlite3 "$WORK/tmp/inspect.db" '.tables' 2>/dev/null)"
    {
      echo "# tables"; printf '%s\n' "$OC_TABLES"
      for t in session message part; do
        echo "# schema $t"; sqlite3 "$WORK/tmp/inspect.db" ".schema $t"
        echo "# count $t"; sqlite3 "$WORK/tmp/inspect.db" "select count(*) from $t;" 2>/dev/null
      done
    } > "$LOGS/opencode-schema.txt" 2>&1
    for t in session message part; do
      if printf '%s\n' "$OC_TABLES" | tr -s ' \t' '\n' | grep -qx "$t"; then
        ok "schema has expected table: $t"
      else
        bad "schema MISSING expected table: $t (adapter assumptions may need updating — see $LOGS/opencode-schema.txt)"
      fi
    done
    note "schema dump saved: $LOGS/opencode-schema.txt"
  else
    note "sqlite3 not on PATH — skipping schema dump (install sqlite3 for the schema check)"
  fi
  # Generated JSONL export must live under /tmp. Count via wc rather than piping
  # find into `grep -q`: grep -q closes the pipe early, which under pipefail
  # surfaces find's SIGPIPE as a spurious failure.
  EXPORT_COUNT="$(find "$WORK/export" -name '*.jsonl' 2>/dev/null | wc -l | tr -d ' ')"
  if [ "${EXPORT_COUNT:-0}" -gt 0 ]; then
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
  ok "source unchanged (path metadata + content fingerprint identical)"
else
  bad "source tree CHANGED during run — investigate before trusting the adapter"
fi

if [ "$SOURCE" = "opencode" ] && [ "${#OC_DBS[@]}" -gt 0 ]; then
  changed=0
  for i in "${!OC_HASH_PATHS[@]}"; do
    after="$(sha256_file "${OC_HASH_PATHS[$i]}")"
    [ "$after" = "${OC_HASH_BEFORE[$i]}" ] || { changed=1; bad "OpenCode source content changed: ${OC_HASH_PATHS[$i]}"; }
  done
  if [ "$changed" -eq 0 ]; then
    ok "opencode database/sidecar content unchanged (sha256 identical)"
    note "if this ever fails while OpenCode is running, re-run with OpenCode closed"
  fi
  if command -v sqlite3 >/dev/null 2>&1; then
    for i in "${!OC_DBS[@]}"; do
      d="${OC_DBS[$i]}"
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
