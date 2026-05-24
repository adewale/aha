package corpus

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

type VerifyProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Count   int    `json:"count,omitempty"`
}

type VerifyReport struct {
	Root     string          `json:"root"`
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
	checks := []struct {
		code    string
		message string
		query   string
	}{
		{"orphan_messages", "messages without backing entries", `select count(*) from messages m left join entries e on e.session_key=m.session_key and e.entry_id=m.entry_id where e.session_key is null`},
		{"orphan_entry_assets", "entry_assets without backing entries", `select count(*) from entry_assets a left join entries e on e.session_key=a.session_key and e.entry_id=a.entry_id where e.session_key is null`},
		{"orphan_artifacts", "artifacts without backing bundles", `select count(*) from artifacts a left join bundles b on b.bundle_id=a.bundle_id where b.bundle_id is null`},
		{"orphan_fts_messages", "fts_messages rows without backing messages", `select count(*) from fts_messages f left join messages m on m.session_key=f.session_key and m.entry_id=f.entry_id where m.session_key is null`},
		{"missing_fts_messages", "messages without fts_messages rows", `select count(*) from messages m left join fts_messages f on f.session_key=m.session_key and f.entry_id=m.entry_id where f.session_key is null`},
		{"orphan_fts_artifacts", "fts_artifacts rows without backing artifacts", `select count(*) from fts_artifacts f left join artifacts a on a.artifact_id=f.artifact_id where a.artifact_id is null`},
		{"missing_fts_artifacts", "artifacts without fts_artifacts rows", `select count(*) from artifacts a left join fts_artifacts f on f.artifact_id=a.artifact_id where trim(coalesce(nullif(a.text_body,''),a.text_preview,''))<>'' and f.artifact_id is null`},
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
	rows, err := store.DB.Query(`select bundle_id,bundle_sha256 from bundles where bundle_sha256<>''`)
	if err != nil {
		return report, err
	}
	defer rows.Close()
	missingBundles := 0
	for rows.Next() {
		var id, sha string
		if err := rows.Scan(&id, &sha); err != nil {
			return report, err
		}
		if _, err := os.Stat(filepath.Join(store.Root, "blobs", "bundles", sha+".tar.zst")); err != nil {
			if os.IsNotExist(err) {
				missingBundles++
				continue
			}
			return report, fmt.Errorf("stat bundle blob %s/%s: %w", id, sha, err)
		}
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	if missingBundles > 0 {
		report.Problems = append(report.Problems, VerifyProblem{Code: "missing_bundle_blob", Message: "bundle rows without promoted bundle blobs", Count: missingBundles})
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
