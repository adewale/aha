package search

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

func TestActualSearchSQLUsesIndexedExactFilters(t *testing.T) {
	store, err := corpus.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	cases := []struct {
		name string
		sql  string
		vals []any
		want []string
	}{
		{name: "message project path token", want: []string{"idx_session_path_tokens_token_session", "idx_sessions_project"}},
		{name: "artifact path token", want: []string{"idx_artifact_path_tokens_token_artifact"}},
	}
	cases[0].sql, cases[0].vals = messageSQL("needle", Filters{Project: "project-001", PathToken: "project-001", Limit: 10})
	cases[1].sql, cases[1].vals = artifactSQL("needle", Filters{PathToken: "project-001", Limit: 10})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := explainPlan(t, store, tc.sql, tc.vals...)
			for _, want := range tc.want {
				if !strings.Contains(plan, want) {
					t.Fatalf("plan does not mention %s:\n%s", want, plan)
				}
			}
		})
	}
}

func explainPlan(t *testing.T, store *corpus.Store, query string, args ...any) string {
	t.Helper()
	rows, err := store.DB.Query("explain query plan "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		out.WriteString(detail)
		out.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}
