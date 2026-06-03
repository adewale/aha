// Package opencodeexport converts an OpenCode SQLite database into
// deterministic, lossless JSONL session files that the read-only `opencode`
// source adapter can then discover and parse like any other JSONL source.
//
// It lives outside the adapters package on purpose: source adapters are held
// to a textual read-only invariant (see internal/adapters/read_only_test.go)
// and must not contain filesystem-mutation calls. The conversion does write
// files, but only ever under the caller-provided destination directory and a
// private work area beneath it; it never writes to the OpenCode database.
//
// To stay consistent against a live database that may be using WAL, the source
// DB (plus any -wal/-shm sidecars) is copied into the work area first and the
// copy is queried. The original database is opened only for reading bytes.
package opencodeexport

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

// Session describes one exported OpenCode session JSONL file.
type Session struct {
	ID       string // OpenCode session id (row id)
	File     string // base filename written under destDir
	Path     string // absolute path to the written .jsonl file
	Messages int    // number of message records written
}

// candidate foreign-key / ordering column names, in priority order. OpenCode's
// schema is actively migrating, so the exporter is tolerant about exact names.
var (
	messageSessionFKs = []string{"session_id", "sessionID", "sessionId", "session"}
	partMessageFKs    = []string{"message_id", "messageID", "messageId", "message"}
)

// Run copies the OpenCode database at dbPath into a private work area under
// destDir, then writes one deterministic JSONL file per session into destDir.
// Stale *.jsonl files left by previous runs are pruned so destDir reflects the
// current database. The output is byte-stable for identical database contents.
func Run(ctx context.Context, dbPath, destDir string) ([]Session, error) {
	absDB, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(absDB); err != nil {
		return nil, err
	} else if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("opencode database is not a regular file: %s", absDB)
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return nil, err
	}
	workDir := filepath.Join(destDir, ".work")
	_ = os.RemoveAll(workDir)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return nil, err
	}
	defer os.RemoveAll(workDir)

	copyPath := filepath.Join(workDir, filepath.Base(absDB))
	if err := copyDBWithSidecars(absDB, copyPath); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(copyPath)+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	defer db.Close()

	sessions, err := exportSessions(ctx, db, destDir)
	if err != nil {
		return nil, err
	}
	if err := pruneStale(destDir, sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

func exportSessions(ctx context.Context, db *sql.DB, destDir string) ([]Session, error) {
	if ok, err := tableExists(ctx, db, "session"); err != nil || !ok {
		return nil, err
	}
	sessionRows, err := queryMaps(ctx, db, "SELECT * FROM session ORDER BY id")
	if err != nil {
		return nil, err
	}
	hasMessage, err := tableExists(ctx, db, "message")
	if err != nil {
		return nil, err
	}
	msgFK, msgOrder, err := pickColumns(ctx, db, hasMessage, "message", messageSessionFKs)
	if err != nil {
		return nil, err
	}
	hasPart, err := tableExists(ctx, db, "part")
	if err != nil {
		return nil, err
	}
	partFK, partOrder, err := pickColumns(ctx, db, hasPart, "part", partMessageFKs)
	if err != nil {
		return nil, err
	}

	used := map[string]bool{}
	out := make([]Session, 0, len(sessionRows))
	for _, srow := range sessionRows {
		sid := rowString(srow, "id")
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(map[string]any{"type": "session", "id": sid, "row": srow}); err != nil {
			return nil, err
		}
		msgCount := 0
		if hasMessage && msgFK != "" {
			msgs, err := queryMaps(ctx, db, "SELECT * FROM "+ident("message")+" WHERE "+ident(msgFK)+" = ? ORDER BY "+ident(msgOrder), sid)
			if err != nil {
				return nil, err
			}
			for _, mrow := range msgs {
				mid := rowString(mrow, "id")
				parts := []map[string]any{}
				if hasPart && partFK != "" {
					parts, err = queryMaps(ctx, db, "SELECT * FROM "+ident("part")+" WHERE "+ident(partFK)+" = ? ORDER BY "+ident(partOrder), mid)
					if err != nil {
						return nil, err
					}
				}
				if err := enc.Encode(map[string]any{"type": "message", "id": mid, "row": mrow, "parts": parts}); err != nil {
					return nil, err
				}
				msgCount++
			}
		}
		name := uniqueFileName(sid, used)
		path := filepath.Join(destDir, name)
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			return nil, err
		}
		out = append(out, Session{ID: sid, File: name, Path: path, Messages: msgCount})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// pickColumns resolves the foreign-key and ordering column for a child table,
// tolerating schema variation. Ordering prefers an "id" column for stable,
// roughly-chronological output (OpenCode ids are time-sortable).
func pickColumns(ctx context.Context, db *sql.DB, exists bool, table string, fkCandidates []string) (fk, order string, err error) {
	if !exists {
		return "", "", nil
	}
	cols, err := tableColumns(ctx, db, table)
	if err != nil {
		return "", "", err
	}
	have := map[string]bool{}
	for _, c := range cols {
		have[c] = true
	}
	for _, c := range fkCandidates {
		if have[c] {
			fk = c
			break
		}
	}
	order = fk
	if have["id"] {
		order = "id"
	}
	return fk, order, nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var got string
	err := db.QueryRowContext(ctx, "SELECT name FROM sqlite_master WHERE type='table' AND name = ?", name).Scan(&got)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return got == name, nil
}

func tableColumns(ctx context.Context, db *sql.DB, table string) ([]string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+ident(table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols := rows
	names := []string{}
	colNames, err := cols.Columns()
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		dest := make([]any, len(colNames))
		ptrs := make([]any, len(colNames))
		for i := range dest {
			ptrs[i] = &dest[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		for i, cn := range colNames {
			if cn == "name" {
				if s, ok := asString(dest[i]); ok {
					names = append(names, s)
				}
			}
		}
	}
	return names, rows.Err()
}

// queryMaps runs a query and returns each row as a column->value map. JSON
// object/array text columns are preserved verbatim as json.RawMessage so the
// conversion is lossless; other values keep their native scalar type.
func queryMaps(ctx context.Context, db *sql.DB, query string, args ...any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			m[c] = normalize(vals[i])
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// normalize converts a scanned column value into something json.Marshal can
// emit faithfully. JSON object/array text (which is how OpenCode stores its
// `data` columns) is preserved verbatim as json.RawMessage so the round trip is
// lossless rather than double-encoded. The SQLite driver may hand back TEXT
// columns as either string or []byte, so both are handled.
func normalize(v any) any {
	switch x := v.(type) {
	case nil:
		return nil
	case []byte:
		if raw, ok := embeddedJSON(x); ok {
			return raw
		}
		return string(x)
	case string:
		if raw, ok := embeddedJSON([]byte(x)); ok {
			return raw
		}
		return x
	default:
		return x
	}
}

func embeddedJSON(b []byte) (json.RawMessage, bool) {
	t := bytes.TrimSpace(b)
	if len(t) > 0 && (t[0] == '{' || t[0] == '[') && json.Valid(b) {
		return json.RawMessage(append([]byte(nil), b...)), true
	}
	return nil, false
}

func copyDBWithSidecars(src, dst string) error {
	if err := copyFile(src, dst); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(src + suffix); err == nil {
			if err := copyFile(src+suffix, dst+suffix); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func pruneStale(destDir string, keep []Session) error {
	wanted := map[string]bool{}
	for _, s := range keep {
		wanted[s.File] = true
	}
	entries, err := os.ReadDir(destDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") || wanted[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(destDir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func uniqueFileName(sid string, used map[string]bool) string {
	base := sanitize(sid)
	if base == "" {
		base = "session"
	}
	name := base + ".jsonl"
	for i := 2; used[name]; i++ {
		name = fmt.Sprintf("%s-%d.jsonl", base, i)
	}
	used[name] = true
	return name
}

func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

func rowString(m map[string]any, key string) string {
	s, _ := asString(m[key])
	return s
}

func asString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case []byte:
		return string(x), true
	case int64:
		return fmt.Sprintf("%d", x), true
	case float64:
		return fmt.Sprintf("%g", x), true
	}
	return "", false
}

// ident quotes a SQLite identifier. Table/column names come only from the
// fixed candidate lists and PRAGMA introspection, never from user input, but
// quoting keeps the queries robust against reserved words.
func ident(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
