package search

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/adewale/aha/internal/model"
)

const MaxLimit = 200

type Filters struct {
	Source, Machine, Role, After, Before, Path, Project, PathToken string
	Limit                                                          int
}
type Result struct {
	Score      float64   `json:"score"`
	Timestamp  string    `json:"timestamp"`
	Source     string    `json:"source"`
	Machine    string    `json:"machine"`
	Project    string    `json:"project"`
	Role       string    `json:"role"`
	Snippet    string    `json:"snippet"`
	SessionKey string    `json:"session_key"`
	EntryID    string    `json:"entry_id"`
	Ref        model.Ref `json:"ref"`
	RefText    string    `json:"ref_text"`
}

func Query(db *sql.DB, queryText string, f Filters) ([]Result, error) {
	if f.Limit <= 0 {
		f.Limit = 20
	}
	if f.Limit > MaxLimit {
		f.Limit = MaxLimit
	}
	q := ftsQuery(queryText)
	var results []Result
	sqlText, vals := messageSQL(q, f)
	rows, err := db.Query(sqlText, vals...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.Score, &r.Timestamp, &r.Source, &r.Machine, &r.Project, &r.Role, &r.Snippet, &r.SessionKey, &r.EntryID); err != nil {
			return nil, err
		}
		ref, err := messageRef(r.SessionKey, r.EntryID)
		if err != nil {
			return nil, err
		}
		r.Ref = ref
		r.RefText = model.FormatRef(ref)
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

type predicateSpec struct {
	sourceColumn    string
	machineColumn   string
	afterColumn     string
	beforeColumn    string
	pathColumn      string
	pathTokenExists string
	projectExists   string
}

func appendFilterPredicates(where []string, vals []any, f Filters, spec predicateSpec) ([]string, []any) {
	if f.Source != "" {
		where = append(where, spec.sourceColumn+"=?")
		vals = append(vals, f.Source)
	}
	if f.Machine != "" {
		where = append(where, spec.machineColumn+"=?")
		vals = append(vals, f.Machine)
	}
	if f.After != "" {
		where = append(where, spec.afterColumn+">=?")
		vals = append(vals, f.After)
	}
	if f.Before != "" {
		where = append(where, spec.beforeColumn+"<=?")
		vals = append(vals, f.Before)
	}
	if f.Path != "" {
		where = append(where, spec.pathColumn+" like ? escape '\\'")
		vals = append(vals, likeContains(f.Path))
	}
	if f.PathToken != "" {
		where = append(where, spec.pathTokenExists)
		vals = append(vals, normalizeToken(f.PathToken))
	}
	if f.Project != "" {
		where = append(where, spec.projectExists)
		vals = append(vals, f.Project)
	}
	return where, vals
}

func messageSQL(q string, f Filters) (string, []any) {
	where := []string{"fts_messages match ?"}
	vals := []any{q}
	if f.Role != "" {
		where = append(where, "m.role=?")
		vals = append(vals, f.Role)
	}
	where, vals = appendFilterPredicates(where, vals, f, predicateSpec{
		sourceColumn:    "s.source_name",
		machineColumn:   "s.machine_id",
		afterColumn:     "e.timestamp",
		beforeColumn:    "e.timestamp",
		pathColumn:      "s.raw_cwd",
		pathTokenExists: "exists(select 1 from session_path_tokens spt indexed by idx_session_path_tokens_token_session where spt.token=? and spt.session_key=s.session_key)",
		projectExists:   "exists(select 1 from sessions sp indexed by idx_sessions_project where sp.project_key=? and sp.session_key=s.session_key)",
	})
	vals = append(vals, f.Limit)
	return `select bm25(fts_messages) score,e.timestamp,s.source_name,s.machine_id,coalesce(s.raw_cwd,''),m.role,snippet(fts_messages,2,'[',']','…',12),m.session_key,m.entry_id from fts_messages join messages m on m.session_key=fts_messages.session_key and m.entry_id=fts_messages.entry_id join sessions s on s.session_key=m.session_key join entries e on e.session_key=m.session_key and e.entry_id=m.entry_id where ` + strings.Join(where, " and ") + ` order by score,e.timestamp,m.session_key,m.entry_id limit ?`, vals
}

func artifactSQL(q string, f Filters) (string, []any) {
	where := []string{"fts_artifacts match ?"}
	vals := []any{q}
	where, vals = appendFilterPredicates(where, vals, f, predicateSpec{
		sourceColumn:    "a.source_name",
		machineColumn:   "a.machine_id",
		afterColumn:     "b.captured_at",
		beforeColumn:    "b.captured_at",
		pathColumn:      "a.raw_path",
		pathTokenExists: "exists(select 1 from artifact_path_tokens apt indexed by idx_artifact_path_tokens_token_artifact where apt.token=? and apt.artifact_id=a.artifact_id)",
		projectExists:   "exists(select 1 from sessions psp indexed by idx_sessions_project where psp.project_key=? and psp.session_key=a.parent_session_key)",
	})
	vals = append(vals, f.Limit)
	return `select bm25(fts_artifacts) score,coalesce(b.captured_at,''),a.source_name,a.machine_id,a.raw_path,snippet(fts_artifacts,1,'[',']','…',12),coalesce(a.parent_session_key,''),a.artifact_sha256 from fts_artifacts join artifacts a on a.artifact_id=fts_artifacts.artifact_id left join snapshots b on b.manifest_sha256=a.manifest_sha256 left join sessions ps on ps.session_key=a.parent_session_key where ` + strings.Join(where, " and ") + ` order by score,coalesce(b.captured_at,''),a.raw_path,a.artifact_sha256 limit ?`, vals
}

func queryArtifacts(db *sql.DB, q string, f Filters) ([]Result, error) {
	sqlText, vals := artifactSQL(q, f)
	rows, err := db.Query(sqlText, vals...)
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
		sha, err := model.ParseSHA256Hex(artifactSHA)
		if err != nil {
			return nil, err
		}
		ref := model.ArtifactRef{SHA: sha}
		r.Ref = ref
		r.RefText = model.FormatRef(ref)
		r.SessionKey, r.EntryID = parentSession, artifactSHA
		r.Role = "artifact"
		out = append(out, r)
	}
	return out, rows.Err()
}

func messageRef(sessionKey, entryID string) (model.MessageRef, error) {
	session, err := model.ParseSessionKey(sessionKey)
	if err != nil {
		return model.MessageRef{}, err
	}
	entry, err := model.NewEntryID(entryID)
	if err != nil {
		return model.MessageRef{}, err
	}
	return model.MessageRef{Session: session, Entry: entry}, nil
}

func normalizeToken(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func likeContains(q string) string {
	out := make([]byte, 0, len(q)+2)
	out = append(out, '%')
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
