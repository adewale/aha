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

func ReadRef(db *sql.DB, ref model.HitRef, before, after int) ([]ReadEntry, error) {
	if ref.Kind == model.HitKindArtifact {
		sha := firstNonEmpty(ref.ArtifactSHA, ref.EntryID)
		sessionKey := ref.SessionKey
		if _, ok := model.ParseArtifactSessionKey(sessionKey); ok {
			sessionKey = ""
		}
		artifact, err := readArtifactHit(db, sessionKey, sha)
		if err != nil {
			return nil, err
		}
		return []ReadEntry{artifact}, nil
	}
	return ReadContext(db, ref.SessionKey, ref.EntryID, before, after)
}

func ReadCanonical(db *sql.DB, ref model.HitRef, before, after int) ([]ReadEntry, error) {
	if ref.Kind == model.HitKindArtifact {
		return ReadRef(db, ref, before, after)
	}
	var sessionKey string
	if err := db.QueryRow(`select session_key from sessions where session_key=?`, ref.SessionKey).Scan(&sessionKey); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("session not found: %s", ref.SessionKey)
		}
		return nil, err
	}
	center := 1
	if ref.EntryID != "" {
		if err := db.QueryRow(`select line_no from entries where session_key=? and entry_id=?`, sessionKey, ref.EntryID).Scan(&center); err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("entry not found: %s", ref.EntryID)
			}
			return nil, err
		}
	}
	return readWindow(db, sessionKey, center, before, after)
}

func ResolveHuman(db *sql.DB, session, entry string) (model.HitRef, error) {
	if sha, ok := model.ParseArtifactSessionKey(session); ok {
		if entry != "" {
			sha = entry
		}
		return model.HitRef{Kind: model.HitKindArtifact, SessionKey: model.ArtifactSessionKey(sha), EntryID: sha, ArtifactSHA: sha}, nil
	}
	sk, err := resolveSession(db, session)
	if err != nil {
		return model.HitRef{}, err
	}
	if entry == "" {
		return model.HitRef{Kind: model.HitKindMessage, SessionKey: sk}, nil
	}
	entryID, err := resolveEntryID(db, sk, entry)
	if err != nil {
		return model.HitRef{}, err
	}
	return model.HitRef{Kind: model.HitKindMessage, SessionKey: sk, EntryID: entryID}, nil
}

func ReadContext(db *sql.DB, session, entry string, before, after int) ([]ReadEntry, error) {
	if parsedSHA, ok := model.ParseArtifactSessionKey(session); ok {
		sha := parsedSHA
		if entry != "" {
			sha = entry
		}
		artifact, err := readArtifactHit(db, "", sha)
		if err != nil {
			return nil, err
		}
		return []ReadEntry{artifact}, nil
	}
	sk, err := resolveSession(db, session)
	if err != nil {
		return nil, err
	}
	center := 1
	if entry != "" {
		center, err = resolveEntryLine(db, sk, entry)
		if err != nil {
			if artifact, artifactErr := readArtifactHit(db, sk, entry); artifactErr == nil {
				return []ReadEntry{artifact}, nil
			}
			return nil, err
		}
	}
	return readWindow(db, sk, center, before, after)
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
		return "", fmt.Errorf("ambiguous session %q", q)
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
		return "", fmt.Errorf("ambiguous session %q", q)
	}
	return "", fmt.Errorf("session not found: %s", q)
}

func resolveEntryLine(db *sql.DB, sessionKey, q string) (int, error) {
	entryID, err := resolveEntryID(db, sessionKey, q)
	if err != nil {
		return 0, err
	}
	var line int
	if err := db.QueryRow(`select line_no from entries where session_key=? and entry_id=?`, sessionKey, entryID).Scan(&line); err != nil {
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
			return "", fmt.Errorf("ambiguous entry %q", q)
		}
	}
	return "", fmt.Errorf("entry not found: %s", q)
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
