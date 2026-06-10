package corpus

import (
	"database/sql"
	"path/filepath"
)

type VerifyProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
}

type VerifyStats struct {
	Messages        int `json:"messages"`
	Artifacts       int `json:"artifacts"`
	FTSMessages     int `json:"fts_messages"`
	FTSArtifacts    int `json:"fts_artifacts"`
	Snapshots       int `json:"snapshots"`
	Redactions      int `json:"redactions"`
	RedactionEvents int `json:"redaction_events"`
	ToolInvocations int `json:"tool_invocations"`
}

type VerifyReport struct {
	Root     string          `json:"root"`
	Stats    VerifyStats     `json:"stats"`
	Problems []VerifyProblem `json:"problems,omitempty"`
}

func (r VerifyReport) HasProblem(code string) bool {
	for _, p := range r.Problems {
		if p.Code == code {
			return true
		}
	}
	return false
}

func Verify(store *Store) (VerifyReport, error) {
	report := VerifyReport{Root: filepath.Clean(store.Root)}
	stats := []struct {
		dst   *int
		query string
	}{
		{&report.Stats.Messages, `select count(*) from messages`},
		{&report.Stats.Artifacts, `select count(*) from artifacts`},
		{&report.Stats.FTSMessages, `select count(*) from fts_messages`},
		{&report.Stats.FTSArtifacts, `select count(*) from fts_artifacts`},
		{&report.Stats.Snapshots, `select count(*) from snapshots`},
		{&report.Stats.Redactions, `select count(*) from redactions`},
		{&report.Stats.RedactionEvents, `select count(*) from redaction_events`},
		{&report.Stats.ToolInvocations, `select count(*) from tool_invocations`},
	}
	for _, stat := range stats {
		count, err := verifyCount(store.DB, stat.query)
		if err != nil {
			return report, err
		}
		*stat.dst = count
	}
	checks := []struct {
		code    string
		message string
		query   string
	}{
		{"orphan_messages", "messages without backing entries", `select count(*) from messages m left join entries e on e.session_key=m.session_key and e.entry_id=m.entry_id where e.session_key is null`},
		{"orphan_entry_assets", "entry_assets without backing entries", `select count(*) from entry_assets a left join entries e on e.session_key=a.session_key and e.entry_id=a.entry_id where e.session_key is null`},
		{"orphan_artifacts", "artifacts without backing snapshots", `select count(*) from artifacts a left join snapshots s on s.manifest_sha256=a.manifest_sha256 where s.manifest_sha256 is null`},
		{"orphan_fts_messages", "fts_messages rows without backing messages", `select count(*) from fts_messages f left join messages m on m.rowid=f.rowid where m.rowid is null`},
		{"missing_fts_messages", "messages without fts_messages rows", missingFTSMessagesQuery},
		{"orphan_fts_artifacts", "fts_artifacts rows without backing artifacts", `select count(*) from fts_artifacts f left join artifacts a on a.artifact_id=f.rowid where a.artifact_id is null`},
		{"missing_fts_artifacts", "artifacts without fts_artifacts rows", missingFTSArtifactsQuery},
		{"orphan_tool_invocations", "tool_invocations without backing entries", `select count(*) from tool_invocations t left join entries e on e.session_key=t.session_key and e.entry_id=t.entry_id where e.session_key is null`},
		{"orphan_redactions", "redactions without backing entries", `select count(*) from redactions r left join entries e on e.session_key=r.session_key and e.entry_id=r.entry_id where e.session_key is null`},
		{"orphan_redaction_events", "redaction events with missing subjects", `select count(*) from redaction_events r where (r.subject_kind='session' and not exists(select 1 from sessions s where s.session_key=r.subject_id)) or (r.subject_kind='entry' and not exists(select 1 from entries e where e.session_key=r.session_key and e.entry_id=r.entry_id)) or (r.subject_kind='artifact' and r.artifact_id is not null and not exists(select 1 from artifacts a where a.artifact_id=r.artifact_id))`},
		{"unknown_redaction_levels", "sessions with unknown redaction_level", `select count(*) from sessions where coalesce(redaction_level,'none-v1') not in ('none-v1','v1')`},
	}
	for _, check := range checks {
		count, err := verifyCount(store.DB, check.query)
		if err != nil {
			return report, err
		}
		if count > 0 {
			report.Problems = append(report.Problems, VerifyProblem{Code: check.code, Message: check.message, Count: count})
		}
	}
	return report, nil
}

func verifyCount(db *sql.DB, query string) (int, error) {
	var count int
	if err := db.QueryRow(query).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
