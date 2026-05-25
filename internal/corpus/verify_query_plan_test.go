package corpus_test

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

func TestVerifyFTSChecksUseRowIDLookups(t *testing.T) {
	store, _ := corpusWithOneEntry(t)
	defer store.Close()
	queries := map[string]string{
		"missing messages":  `explain query plan select count(*) from messages m left join fts_messages f on f.rowid=m.rowid where trim(coalesce(m.text,''))<>'' and f.rowid is null`,
		"orphan messages":   `explain query plan select count(*) from fts_messages f left join messages m on m.rowid=f.rowid where m.rowid is null`,
		"missing artifacts": `explain query plan select count(*) from artifacts a left join fts_artifacts f on f.rowid=a.artifact_id where trim(coalesce(nullif(a.text_body,''),a.text_preview,''))<>'' and f.rowid is null`,
		"orphan artifacts":  `explain query plan select count(*) from fts_artifacts f left join artifacts a on a.artifact_id=f.rowid where a.artifact_id is null`,
	}
	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			plan := explainPlan(t, store, query)
			if !strings.Contains(plan, "=rowid") && !strings.Contains(plan, "rowid=") && !strings.Contains(plan, "INTEGER PRIMARY KEY") && !strings.Contains(plan, "VIRTUAL TABLE INDEX 0:=") {
				t.Fatalf("verify FTS plan does not show rowid lookup:\n%s", plan)
			}
			for _, bad := range []string{"session_key", "entry_id", "artifact_id"} {
				if strings.Contains(plan, bad+"=") || strings.Contains(plan, "="+bad) {
					t.Fatalf("verify FTS plan appears to join on unindexed FTS column %s:\n%s", bad, plan)
				}
			}
		})
	}
}

func explainPlan(t *testing.T, store *corpus.Store, query string) string {
	t.Helper()
	rows, err := store.DB.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		b.WriteString(detail)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return b.String()
}
