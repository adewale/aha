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
// For a database that may be using WAL, the source DB (plus any currently
// present -wal/-shm sidecars) is copied into the work area first and the copy is
// queried. The original database is opened only for reading bytes. If OpenCode
// checkpoints sidecars during the byte copy, the exporter tolerates disappearing
// sidecars and leaves source files untouched; rerun with OpenCode closed for the
// strongest read-only smoke-test evidence.
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
	"strconv"
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	absDB, err := filepath.Abs(dbPath)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(absDB); err != nil {
		return nil, err
	} else if !st.Mode().IsRegular() {
		return nil, fmt.Errorf("opencode database is not a regular file: %s", absDB)
	}
	maxBytes, err := maxDBCopyBytes()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(destDir, 0o700); err != nil {
		return nil, err
	}

	var sessions []Session
	err = withExportLock(ctx, destDir, func() error {
		workDir, err := os.MkdirTemp(destDir, ".work-")
		if err != nil {
			return err
		}
		if err := os.Chmod(workDir, 0o700); err != nil {
			_ = os.RemoveAll(workDir)
			return err
		}
		defer os.RemoveAll(workDir)

		copyPath := filepath.Join(workDir, filepath.Base(absDB))
		if err := copyDBWithSidecars(ctx, absDB, copyPath, maxBytes); err != nil {
			return err
		}

		db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(copyPath)+"?_pragma=busy_timeout(5000)")
		if err != nil {
			return err
		}
		db.SetMaxOpenConns(4)
		defer db.Close()
		if err := verifyCopiedDB(ctx, db); err != nil {
			return err
		}

		sessions, err = exportSessions(ctx, db, destDir)
		if err != nil {
			return err
		}
		return pruneStale(destDir, sessions)
	})
	if err != nil {
		return nil, err
	}
	return sessions, nil
}

const defaultMaxDBCopyBytes int64 = 8 << 30 // 8 GiB across DB + WAL + SHM.

func maxDBCopyBytes() (int64, error) {
	raw := strings.TrimSpace(os.Getenv("AHA_OPENCODE_MAX_DB_BYTES"))
	if raw == "" {
		return defaultMaxDBCopyBytes, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid AHA_OPENCODE_MAX_DB_BYTES %q", raw)
	}
	return n, nil
}

func verifyCopiedDB(ctx context.Context, db *sql.DB) error {
	var got string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&got); err != nil {
		return err
	}
	if strings.TrimSpace(got) != "ok" {
		return fmt.Errorf("opencode copied database failed quick_check: %s", got)
	}
	return nil
}

func exportSessions(ctx context.Context, db *sql.DB, destDir string) ([]Session, error) {
	if ok, err := tableExists(ctx, db, "session"); err != nil || !ok {
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
	var out []Session
	err = streamMaps(ctx, db, "SELECT * FROM session ORDER BY id", func(srow map[string]any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		sid := rowString(srow, "id")
		name := uniqueFileName(sid, used)
		path := filepath.Join(destDir, name)
		aw, err := newAtomicFile(path, 0o600)
		if err != nil {
			return err
		}
		committed := false
		defer func() {
			if !committed {
				_ = aw.Abort()
			}
		}()
		enc := json.NewEncoder(aw)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(map[string]any{"type": "session", "id": sid, "row": srow}); err != nil {
			return err
		}
		msgCount := 0
		if hasMessage && msgFK != "" {
			if err := streamMaps(ctx, db, "SELECT * FROM "+ident("message")+" WHERE "+ident(msgFK)+" = ? ORDER BY "+ident(msgOrder), func(mrow map[string]any) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				mid := rowString(mrow, "id")
				parts := []map[string]any{}
				if hasPart && partFK != "" {
					parts, err = collectMaps(ctx, db, "SELECT * FROM "+ident("part")+" WHERE "+ident(partFK)+" = ? ORDER BY "+ident(partOrder), mid)
					if err != nil {
						return err
					}
				}
				if err := enc.Encode(map[string]any{"type": "message", "id": mid, "row": mrow, "parts": parts}); err != nil {
					return err
				}
				msgCount++
				return nil
			}, sid); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := aw.Commit(); err != nil {
			return err
		}
		committed = true
		out = append(out, Session{ID: sid, File: name, Path: path, Messages: msgCount})
		return nil
	})
	if err != nil {
		return nil, err
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

// streamMaps runs a query and streams each row as a column->value map. JSON
// object/array text columns are preserved verbatim as json.RawMessage so the
// conversion is lossless; other values keep their native scalar type.
func streamMaps(ctx context.Context, db *sql.DB, query string, fn func(map[string]any) error, args ...any) error {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return err
	}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		m, err := scanMap(rows, cols)
		if err != nil {
			return err
		}
		if err := fn(m); err != nil {
			return err
		}
	}
	return rows.Err()
}

func collectMaps(ctx context.Context, db *sql.DB, query string, args ...any) ([]map[string]any, error) {
	var out []map[string]any
	err := streamMaps(ctx, db, query, func(m map[string]any) error {
		out = append(out, m)
		return nil
	}, args...)
	return out, err
}

func scanMap(rows *sql.Rows, cols []string) (map[string]any, error) {
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
	return m, nil
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

func copyDBWithSidecars(ctx context.Context, src, dst string, maxBytes int64) error {
	var copied int64
	n, err := copyFile(ctx, src, dst, maxBytes)
	if err != nil {
		return err
	}
	copied += n
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := ctx.Err(); err != nil {
			return err
		}
		sidecar := src + suffix
		if _, err := os.Stat(sidecar); err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		n, err := copyFile(ctx, sidecar, dst+suffix, maxBytes-copied)
		if err != nil {
			if os.IsNotExist(err) {
				continue // sidecar disappeared between stat and open/checkpoint.
			}
			return err
		}
		copied += n
	}
	return nil
}

func copyFile(ctx context.Context, src, dst string, remaining int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	removeOnErr := true
	defer func() {
		if removeOnErr {
			_ = os.Remove(dst)
		}
	}()
	buf := make([]byte, 1024*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			_ = out.Close()
			return written, err
		}
		nr, rerr := in.Read(buf)
		if nr > 0 {
			written += int64(nr)
			if remaining >= 0 && written > remaining {
				_ = out.Close()
				return written, fmt.Errorf("opencode database copy exceeds configured byte limit")
			}
			if _, err := out.Write(buf[:nr]); err != nil {
				_ = out.Close()
				return written, err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			_ = out.Close()
			return written, rerr
		}
	}
	if err := out.Close(); err != nil {
		return written, err
	}
	removeOnErr = false
	return written, nil
}

type atomicFile struct {
	path string
	tmp  string
	perm os.FileMode
	f    *os.File
}

func newAtomicFile(path string, perm os.FileMode) (*atomicFile, error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(f.Name(), perm); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return nil, err
	}
	return &atomicFile{path: path, tmp: f.Name(), perm: perm, f: f}, nil
}

func (w *atomicFile) Write(p []byte) (int, error) {
	return w.f.Write(p)
}

func (w *atomicFile) Commit() error {
	if err := w.f.Sync(); err != nil {
		_ = w.f.Close()
		return err
	}
	if err := w.f.Close(); err != nil {
		return err
	}
	w.f = nil
	if err := os.Rename(w.tmp, w.path); err != nil {
		return err
	}
	return os.Chmod(w.path, w.perm)
}

func (w *atomicFile) Abort() error {
	if w.f != nil {
		_ = w.f.Close()
		w.f = nil
	}
	return os.Remove(w.tmp)
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
	base := sanitise(sid)
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

func sanitise(s string) string {
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
