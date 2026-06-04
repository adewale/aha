package corpus

import (
	"database/sql"
	"fmt"

	"github.com/adewale/aha/internal/model"
)

type ReadEntry struct {
	LineNo    int    `json:"line_no"`
	EntryID   string `json:"entry_id"`
	Timestamp string `json:"timestamp"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	RawJSON   string `json:"raw_json"`
}

func ReadRef(db *sql.DB, ref model.Ref, before, after int) ([]ReadEntry, error) {
	return ReadCanonical(db, ref, before, after)
}

func ReadCanonical(db *sql.DB, ref model.Ref, before, after int) ([]ReadEntry, error) {
	if ref == nil || !ref.Valid() {
		return nil, fmt.Errorf("invalid ref")
	}
	switch r := ref.(type) {
	case model.ArtifactRef:
		artifact, err := readArtifactHit(db, "", r.SHA.String())
		if err != nil {
			return nil, err
		}
		return []ReadEntry{artifact}, nil
	case model.SessionRef:
		if err := requireSession(db, r.Session.String()); err != nil {
			return nil, err
		}
		return readWindow(db, r.Session.String(), 1, before, after)
	case model.MessageRef:
		center := 1
		if err := db.QueryRow(`select line_no from entries where session_key=? and entry_id=?`, r.Session.String(), r.Entry.String()).Scan(&center); err != nil {
			if err == sql.ErrNoRows {
				return nil, NotFoundError{Kind: "entry", Value: r.Entry.String()}
			}
			return nil, err
		}
		return readWindow(db, r.Session.String(), center, before, after)
	default:
		return nil, fmt.Errorf("unsupported ref variant")
	}
}

func ResolveHuman(db *sql.DB, session, entry string) (model.Ref, error) {
	sk, err := resolveSession(db, session)
	if err != nil {
		return nil, err
	}
	sessionKey, err := model.ParseSessionKey(sk)
	if err != nil {
		return nil, err
	}
	if entry == "" {
		return model.SessionRef{Session: sessionKey}, nil
	}
	entryID, err := resolveEntryID(db, sk, entry)
	if err != nil {
		return nil, err
	}
	parsedEntry, err := model.NewEntryID(entryID)
	if err != nil {
		return nil, err
	}
	return model.MessageRef{Session: sessionKey, Entry: parsedEntry}, nil
}

func ReadContext(db *sql.DB, session, entry string, before, after int) ([]ReadEntry, error) {
	ref, err := ResolveHuman(db, session, entry)
	if err != nil {
		return nil, err
	}
	return ReadCanonical(db, ref, before, after)
}

// ReadBranch walks the parent_id chain from leafEntryID back to a root
// (parent_id == "") in the session identified by sessionKeyOrID and
// returns the entries on that path, ordered root → leaf. This is the
// natural "what did the model actually see at this turn" projection for
// Pi sessions where one file holds multiple alternate timelines, and a
// no-op-shaped result for single-thread sessions (the path is just the
// timeline up to the leaf).
//
// Returns NotFoundError if the session or leaf entry does not exist, and
// reports dangling parents or cycles as ordinary errors (the corpus
// fidelity tests guarantee real sessions are well-formed).
func ReadBranch(db *sql.DB, sessionKeyOrID, leafEntryID string) ([]ReadEntry, error) {
	sessionKey, err := resolveSession(db, sessionKeyOrID)
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`select e.line_no, e.entry_id, e.parent_id, e.timestamp, e.role, coalesce(m.text, ''), e.raw_json from entries e left join messages m on m.session_key=e.session_key and m.entry_id=e.entry_id where e.session_key=?`, sessionKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type record struct {
		entry    ReadEntry
		parentID string
	}
	byID := map[string]record{}
	for rows.Next() {
		var r record
		if err := rows.Scan(&r.entry.LineNo, &r.entry.EntryID, &r.parentID, &r.entry.Timestamp, &r.entry.Role, &r.entry.Text, &r.entry.RawJSON); err != nil {
			return nil, err
		}
		byID[r.entry.EntryID] = r
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if _, ok := byID[leafEntryID]; !ok {
		return nil, NotFoundError{Kind: "entry", Value: leafEntryID}
	}
	var path []ReadEntry
	seen := map[string]struct{}{}
	cur := leafEntryID
	for cur != "" {
		if _, dup := seen[cur]; dup {
			return nil, fmt.Errorf("parent_id cycle at %q in session %q", cur, sessionKey)
		}
		seen[cur] = struct{}{}
		r, ok := byID[cur]
		if !ok {
			return nil, fmt.Errorf("dangling parent_id %q in session %q", cur, sessionKey)
		}
		path = append(path, r.entry)
		cur = r.parentID
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, nil
}

func readWindow(db *sql.DB, sessionKey string, center, before, after int) ([]ReadEntry, error) {
	rows, err := db.Query(`select e.line_no,e.entry_id,e.timestamp,e.role,coalesce(m.text,''),e.raw_json from entries e left join messages m on m.session_key=e.session_key and m.entry_id=e.entry_id where e.session_key=? and e.line_no between ? and ? order by e.line_no`, sessionKey, center-before, center+after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadEntry
	for rows.Next() {
		var e ReadEntry
		if err := rows.Scan(&e.LineNo, &e.EntryID, &e.Timestamp, &e.Role, &e.Text, &e.RawJSON); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func requireSession(db *sql.DB, sessionKey string) error {
	var found string
	if err := db.QueryRow(`select session_key from sessions where session_key=?`, sessionKey).Scan(&found); err != nil {
		if err == sql.ErrNoRows {
			return NotFoundError{Kind: "session", Value: sessionKey}
		}
		return err
	}
	return nil
}

func resolveSession(db *sql.DB, q string) (string, error) {
	rows, err := db.Query(`select session_key from sessions where session_key=? or source_session_id=?`, q, q)
	if err != nil {
		return "", err
	}
	matches, err := collectStrings(rows)
	if err != nil {
		return "", err
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", AmbiguousError{Kind: "session", Value: q}
	}
	prefix := likePrefix(q)
	rows, err = db.Query(`select session_key from sessions where session_key like ? escape '\' or source_session_id like ? escape '\'`, prefix, prefix)
	if err != nil {
		return "", err
	}
	matches, err = collectStrings(rows)
	if err != nil {
		return "", err
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return "", AmbiguousError{Kind: "session", Value: q}
	}
	return "", NotFoundError{Kind: "session", Value: q}
}

func resolveEntryLine(db *sql.DB, sessionKey, entry string) (int, error) {
	entryID, err := resolveEntryID(db, sessionKey, entry)
	if err != nil {
		return 0, err
	}
	var line int
	if err := db.QueryRow(`select line_no from entries where session_key=? and entry_id=?`, sessionKey, entryID).Scan(&line); err != nil {
		if err == sql.ErrNoRows {
			return 0, NotFoundError{Kind: "entry", Value: entry}
		}
		return 0, err
	}
	return line, nil
}

func resolveEntryID(db *sql.DB, sessionKey, q string) (string, error) {
	for _, like := range []bool{false, true} {
		arg := q
		sqlq := `select entry_id from entries where session_key=? and entry_id=?`
		if like {
			arg = likePrefix(q)
			sqlq = `select entry_id from entries where session_key=? and entry_id like ? escape '\'`
		}
		rows, err := db.Query(sqlq, sessionKey, arg)
		if err != nil {
			return "", err
		}
		var matches []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return "", err
			}
			matches = append(matches, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return "", err
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return "", AmbiguousError{Kind: "entry", Value: q}
		}
	}
	return "", NotFoundError{Kind: "entry", Value: q}
}

func likePrefix(q string) string {
	out := make([]byte, 0, len(q)+1)
	for i := 0; i < len(q); i++ {
		switch q[i] {
		case '\\', '%', '_':
			out = append(out, '\\')
		}
		out = append(out, q[i])
	}
	out = append(out, '%')
	return string(out)
}

func readArtifactHit(db *sql.DB, sessionKey, artifactSHA string) (ReadEntry, error) {
	var text string
	var rawPath string
	var err error
	if sessionKey == "" {
		err = db.QueryRow(`select coalesce(nullif(text_body,''), text_preview),raw_path from artifacts where artifact_sha256=? order by artifact_id limit 1`, artifactSHA).Scan(&text, &rawPath)
	} else {
		err = db.QueryRow(`select coalesce(nullif(text_body,''), text_preview),raw_path from artifacts where parent_session_key=? and artifact_sha256=? order by artifact_id limit 1`, sessionKey, artifactSHA).Scan(&text, &rawPath)
	}
	if err != nil {
		if err == sql.ErrNoRows {
			return ReadEntry{}, NotFoundError{Kind: "artifact", Value: artifactSHA}
		}
		return ReadEntry{}, err
	}
	return ReadEntry{LineNo: 0, EntryID: artifactSHA, Role: "artifact", Text: text, RawJSON: rawPath}, nil
}

func collectStrings(rows *sql.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
