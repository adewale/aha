package search

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/adewale/aha/internal/model"
)

type Filters struct {
	Source, Machine, Role, After, Before, Path string
	Limit                                      int
}
type Result struct {
	Score      float64      `json:"score"`
	Timestamp  string       `json:"timestamp"`
	Source     string       `json:"source"`
	Machine    string       `json:"machine"`
	Project    string       `json:"project"`
	Role       string       `json:"role"`
	Snippet    string       `json:"snippet"`
	SessionKey string       `json:"session_key"`
	EntryID    string       `json:"entry_id"`
	Ref        model.HitRef `json:"ref"`
	RefText    string       `json:"ref_text"`
}

func Query(db *sql.DB, queryText string, f Filters) ([]Result, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	q := ftsQuery(queryText)
	var results []Result
	where := []string{"fts_messages match ?"}
	vals := []any{q}
	if f.Source != "" {
		where = append(where, "s.source_name=?")
		vals = append(vals, f.Source)
	}
	if f.Machine != "" {
		where = append(where, "s.machine_id=?")
		vals = append(vals, f.Machine)
	}
	if f.Role != "" {
		where = append(where, "m.role=?")
		vals = append(vals, f.Role)
	}
	if f.After != "" {
		where = append(where, "e.timestamp>=?")
		vals = append(vals, f.After)
	}
	if f.Before != "" {
		where = append(where, "e.timestamp<=?")
		vals = append(vals, f.Before)
	}
	if f.Path != "" {
		where = append(where, "s.raw_cwd like ?")
		vals = append(vals, "%"+f.Path+"%")
	}
	vals = append(vals, f.Limit)
	rows, err := db.Query(`select bm25(fts_messages) score,e.timestamp,s.source_name,s.machine_id,coalesce(s.raw_cwd,''),m.role,snippet(fts_messages,2,'[',']','…',12),m.session_key,m.entry_id from fts_messages join messages m on m.session_key=fts_messages.session_key and m.entry_id=fts_messages.entry_id join sessions s on s.session_key=m.session_key join entries e on e.session_key=m.session_key and e.entry_id=m.entry_id where `+strings.Join(where, " and ")+` order by score,e.timestamp,m.session_key,m.entry_id limit ?`, vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.Score, &r.Timestamp, &r.Source, &r.Machine, &r.Project, &r.Role, &r.Snippet, &r.SessionKey, &r.EntryID); err != nil {
			return nil, err
		}
		r.Ref = model.HitRef{Kind: model.HitKindMessage, SessionKey: r.SessionKey, EntryID: r.EntryID}
		r.RefText = model.FormatHitRef(r.Ref)
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if f.Role == "" || f.Role == "artifact" {
		arts, err := queryArtifacts(db, q, f)
		if err != nil {
			return nil, err
		}
		results = append(results, arts...)
	}
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.Score != b.Score {
			return a.Score < b.Score
		}
		if a.Timestamp != b.Timestamp {
			return a.Timestamp < b.Timestamp
		}
		if a.Source != b.Source {
			return a.Source < b.Source
		}
		if a.Machine != b.Machine {
			return a.Machine < b.Machine
		}
		if a.SessionKey != b.SessionKey {
			return a.SessionKey < b.SessionKey
		}
		if a.EntryID != b.EntryID {
			return a.EntryID < b.EntryID
		}
		return a.Role < b.Role
	})
	if len(results) > f.Limit {
		results = results[:f.Limit]
	}
	return results, nil
}

func queryArtifacts(db *sql.DB, q string, f Filters) ([]Result, error) {
	where := []string{"fts_artifacts match ?"}
	vals := []any{q}
	if f.Source != "" {
		where = append(where, "a.source_name=?")
		vals = append(vals, f.Source)
	}
	if f.Machine != "" {
		where = append(where, "a.machine_id=?")
		vals = append(vals, f.Machine)
	}
	if f.Path != "" {
		where = append(where, "a.raw_path like ?")
		vals = append(vals, "%"+f.Path+"%")
	}
	if f.After != "" {
		where = append(where, "b.captured_at>=?")
		vals = append(vals, f.After)
	}
	if f.Before != "" {
		where = append(where, "b.captured_at<=?")
		vals = append(vals, f.Before)
	}
	vals = append(vals, f.Limit)
	rows, err := db.Query(`select bm25(fts_artifacts) score,coalesce(b.captured_at,''),a.source_name,a.machine_id,a.raw_path,snippet(fts_artifacts,1,'[',']','…',12),coalesce(a.parent_session_key,''),a.artifact_sha256 from fts_artifacts join artifacts a on a.artifact_id=fts_artifacts.artifact_id left join bundles b on b.bundle_id=a.bundle_id where `+strings.Join(where, " and ")+` order by score,coalesce(b.captured_at,''),a.raw_path,a.artifact_sha256 limit ?`, vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Result
	for rows.Next() {
		var r Result
		var parentSession, artifactSHA string
		if err := rows.Scan(&r.Score, &r.Timestamp, &r.Source, &r.Machine, &r.Project, &r.Snippet, &parentSession, &artifactSHA); err != nil {
			return nil, err
		}
		ref := model.HitRef{Kind: model.HitKindArtifact, SessionKey: parentSession, ArtifactSHA: artifactSHA, EntryID: artifactSHA}
		if ref.SessionKey == "" {
			ref.SessionKey = model.ArtifactSessionKey(ref.ArtifactSHA)
		}
		r.Ref = ref
		r.RefText = model.FormatHitRef(ref)
		r.SessionKey, r.EntryID = ref.SessionKey, ref.EntryID
		r.Role = "artifact"
		out = append(out, r)
	}
	return out, rows.Err()
}

func ftsQuery(q string) string {
	fields := strings.Fields(q)
	if len(fields) == 0 {
		return q
	}
	out := fields[:0]
	for _, f := range fields {
		f = strings.ReplaceAll(f, `\\`, " ")
		f = strings.ReplaceAll(f, `"`, `""`)
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		out = append(out, fmt.Sprintf("\"%s\"", f))
	}
	return strings.Join(out, " ")
}
