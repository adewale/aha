package corpus

import (
	"database/sql"
	"fmt"
)

func Status(db *sql.DB, root string) (map[string]any, error) {
	stats := map[string]any{"corpus_dir": root}
	for _, table := range []string{"machines", "bundles", "sources", "files", "sessions", "session_versions", "entries", "messages", "artifacts", "images", "entry_assets", "conflicts", "fts_messages", "fts_artifacts"} {
		var n int
		if err := db.QueryRow("select count(*) from " + table).Scan(&n); err != nil {
			return nil, fmt.Errorf("status count %s: %w", table, err)
		}
		stats[table] = n
	}
	var pageCount, pageSize int64
	if err := db.QueryRow(`pragma page_count`).Scan(&pageCount); err != nil {
		return nil, fmt.Errorf("status pragma page_count: %w", err)
	}
	if err := db.QueryRow(`pragma page_size`).Scan(&pageSize); err != nil {
		return nil, fmt.Errorf("status pragma page_size: %w", err)
	}
	stats["index_size_bytes"] = pageCount * pageSize
	return stats, nil
}

type Conflict struct {
	ID         int    `json:"id"`
	SessionKey string `json:"session_key"`
	EntryID    string `json:"entry_id"`
	First      string `json:"first_entry_sha256"`
	Second     string `json:"second_entry_sha256"`
	Details    string `json:"details_json"`
	CreatedAt  string `json:"created_at"`
}

func Conflicts(db *sql.DB) ([]Conflict, error) {
	rows, err := db.Query(`select conflict_id,session_key,entry_id,first_entry_sha256,second_entry_sha256,details_json,created_at from conflicts order by conflict_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conflict
	for rows.Next() {
		var c Conflict
		if err := rows.Scan(&c.ID, &c.SessionKey, &c.EntryID, &c.First, &c.Second, &c.Details, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

var _ *sql.DB
