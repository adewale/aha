package corpus

import "database/sql"

// NamedCount is a label with an associated count, used for corpus composition
// breakdowns (sources, machines, projects).
type NamedCount struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

// Overview is a compact orientation summary of the corpus: what it is made of
// and how healthy it is. It answers "what am I looking at?" before the
// analytical lists. Sourced from cheap grouped queries over sessions plus the
// SQLite page accounting already used by status.
type Overview struct {
	Sessions       int          `json:"sessions"`
	Entries        int          `json:"entries"`
	Messages       int          `json:"messages"`
	ToolCalls      int          `json:"tool_calls"`
	Sources        []NamedCount `json:"sources"`
	Machines       []NamedCount `json:"machines"`
	Projects       []NamedCount `json:"projects"`
	FirstSession   string       `json:"first_session"`
	LastSession    string       `json:"last_session"`
	IndexSizeBytes int64        `json:"index_size_bytes"`
}

// maxOverviewProjects caps the projects breakdown so a corpus with thousands of
// projects doesn't dump them all into the overview.
const maxOverviewProjects = 12

// CorpusOverview computes the orientation summary.
func CorpusOverview(db *sql.DB) (Overview, error) {
	var o Overview
	for _, c := range []struct {
		dest  *int
		query string
	}{
		{&o.Sessions, `select count(*) from sessions`},
		{&o.Entries, `select count(*) from entries`},
		{&o.Messages, `select count(*) from messages`},
		{&o.ToolCalls, `select count(*) from tool_invocations`},
	} {
		if err := db.QueryRow(c.query).Scan(c.dest); err != nil {
			return Overview{}, err
		}
	}

	var err error
	if o.Sources, err = namedCounts(db, `select source_name, count(*) from sessions group by source_name order by count(*) desc, source_name`, 0); err != nil {
		return Overview{}, err
	}
	if o.Machines, err = namedCounts(db, `select machine_id, count(*) from sessions group by machine_id order by count(*) desc, machine_id`, 0); err != nil {
		return Overview{}, err
	}
	if o.Projects, err = namedCounts(db, `select coalesce(nullif(project_key,''),'(none)'), count(*) from sessions group by 1 order by count(*) desc, 1 limit ?`, maxOverviewProjects); err != nil {
		return Overview{}, err
	}

	var first, last sql.NullString
	if err := db.QueryRow(`select min(started_at), max(started_at) from sessions where coalesce(started_at,'') <> ''`).Scan(&first, &last); err != nil {
		return Overview{}, err
	}
	o.FirstSession, o.LastSession = first.String, last.String

	var pageCount, pageSize int64
	if err := db.QueryRow(`pragma page_count`).Scan(&pageCount); err != nil {
		return Overview{}, err
	}
	if err := db.QueryRow(`pragma page_size`).Scan(&pageSize); err != nil {
		return Overview{}, err
	}
	o.IndexSizeBytes = pageCount * pageSize
	return o, nil
}

func namedCounts(db *sql.DB, query string, limit int) ([]NamedCount, error) {
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = db.Query(query, limit)
	} else {
		rows, err = db.Query(query)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NamedCount{}
	for rows.Next() {
		var nc NamedCount
		if err := rows.Scan(&nc.Name, &nc.Count); err != nil {
			return nil, err
		}
		out = append(out, nc)
	}
	return out, rows.Err()
}
