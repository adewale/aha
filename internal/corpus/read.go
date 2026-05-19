package corpus

import (
	"database/sql"
	"fmt"
	"strings"
)

type ReadEntry struct {
	LineNo    int    `json:"line_no"`
	EntryID   string `json:"entry_id"`
	Timestamp string `json:"timestamp"`
	Role      string `json:"role"`
	Text      string `json:"text"`
	RawJSON   string `json:"raw_json"`
}

func ReadContext(db *sql.DB, session, entry string, before, after int) ([]ReadEntry, error) {
	if strings.HasPrefix(session, "artifact:") {
		sha := strings.TrimPrefix(session, "artifact:")
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
	rows, err := db.Query(`select e.line_no,e.entry_id,e.timestamp,e.role,coalesce(m.text,''),e.raw_json from entries e left join messages m on m.session_key=e.session_key and m.entry_id=e.entry_id where e.session_key=? and e.line_no between ? and ? order by e.line_no`, sk, center-before, center+after)
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
	prefix := q + "%"
	rows, err = db.Query(`select session_key from sessions where session_key like ? or source_session_id like ?`, prefix, prefix)
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
	for _, like := range []bool{false, true} {
		arg := q
		sqlq := `select line_no from entries where session_key=? and entry_id=?`
		if like {
			arg = q + "%"
			sqlq = `select line_no from entries where session_key=? and entry_id like ?`
		}
		rows, err := db.Query(sqlq, sessionKey, arg)
		if err != nil {
			return 0, err
		}
		var matches []int
		for rows.Next() {
			var n int
			if err := rows.Scan(&n); err != nil {
				rows.Close()
				return 0, err
			}
			matches = append(matches, n)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return 0, err
		}
		if len(matches) == 1 {
			return matches[0], nil
		}
		if len(matches) > 1 {
			return 0, fmt.Errorf("ambiguous entry %q", q)
		}
	}
	return 0, fmt.Errorf("entry not found: %s", q)
}

func readArtifactHit(db *sql.DB, sessionKey, artifactSHA string) (ReadEntry, error) {
	var text string
	var rawPath string
	var err error
	if sessionKey == "" {
		err = db.QueryRow(`select text_preview,raw_path from artifacts where artifact_sha256=? order by artifact_id limit 1`, artifactSHA).Scan(&text, &rawPath)
	} else {
		err = db.QueryRow(`select text_preview,raw_path from artifacts where parent_session_key=? and artifact_sha256=? order by artifact_id limit 1`, sessionKey, artifactSHA).Scan(&text, &rawPath)
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
