package corpus

import (
	"database/sql"
	"fmt"
)

func Status(db *sql.DB, root string) (map[string]any, error) {
	stats := map[string]any{"corpus_dir": root}
	for _, table := range []string{"machines", "bundles", "sources", "files", "sessions", "session_versions", "entries", "messages", "artifacts", "images", "entry_assets", "session_path_tokens", "artifact_path_tokens", "conflicts", "tool_invocations", "redactions", "redaction_events", "fts_messages", "fts_artifacts"} {
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
	var entryHits, eventHits int
	if err := db.QueryRow(`select coalesce(sum(count),0) from redactions`).Scan(&entryHits); err != nil {
		return nil, fmt.Errorf("status redaction hits: %w", err)
	}
	if err := db.QueryRow(`select coalesce(sum(count),0) from redaction_events`).Scan(&eventHits); err != nil {
		return nil, fmt.Errorf("status redaction event hits: %w", err)
	}
	stats["redaction_hits"] = entryHits + eventHits
	levels, err := redactionLevels(db)
	if err != nil {
		return nil, err
	}
	stats["redaction_levels"] = levels
	patterns, err := redactionsByPattern(db)
	if err != nil {
		return nil, err
	}
	stats["redactions_by_pattern"] = patterns
	return stats, nil
}

func redactionLevels(db *sql.DB) (map[string]int, error) {
	rows, err := db.Query(`select coalesce(redaction_level,'none-v1'),count(*) from sessions group by coalesce(redaction_level,'none-v1') order by 1`)
	if err != nil {
		return nil, fmt.Errorf("status redaction levels: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var level string
		var count int
		if err := rows.Scan(&level, &count); err != nil {
			return nil, err
		}
		out[level] = count
	}
	return out, rows.Err()
}

func redactionsByPattern(db *sql.DB) (map[string]int, error) {
	rows, err := db.Query(`select pattern,sum(count) from (select pattern,count from redactions union all select pattern,count from redaction_events) group by pattern order by pattern`)
	if err != nil {
		return nil, fmt.Errorf("status redactions by pattern: %w", err)
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var pattern string
		var count int
		if err := rows.Scan(&pattern, &count); err != nil {
			return nil, err
		}
		out[pattern] = count
	}
	return out, rows.Err()
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

func BundleSHAs(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`select bundle_sha256 from bundles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			return nil, err
		}
		out[sha] = true
	}
	return out, rows.Err()
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
