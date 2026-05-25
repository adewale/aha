package search_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/search"
)

func BenchmarkPathologicalQueryBroadTermPathFilter(b *testing.B) {
	messages := searchPathologicalScale("AHA_PATHOLOGICAL_SEARCH_MESSAGES", 10000)
	store := seedSearchPathologicalCorpus(b, messages)
	defer store.Close()
	cases := []struct {
		name    string
		filters search.Filters
	}{
		{name: "broad-term-limit-20", filters: search.Filters{Limit: 20}},
		{name: "broad-term-limit-1000", filters: search.Filters{Limit: 1000}},
		{name: "path-filter-rare-match", filters: search.Filters{Path: "rare/pathological-target", Limit: 20}},
		{name: "path-token-rare-match", filters: search.Filters{PathToken: "pathological-target", Limit: 20}},
		{name: "project-filter-rare-match", filters: search.Filters{Project: "pathological-target", Limit: 20}},
		{name: "path-filter-no-match", filters: search.Filters{Path: "definitely-not-present", Limit: 20}},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := search.Query(store.DB, "pathological", tc.filters); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func seedSearchPathologicalCorpus(tb testing.TB, messages int) *corpus.Store {
	tb.Helper()
	store, err := corpus.Open(filepath.Join(tb.TempDir(), "corpus"))
	if err != nil {
		tb.Fatal(err)
	}
	tx, err := store.DB.Begin()
	if err != nil {
		store.Close()
		tb.Fatal(err)
	}
	fail := func(err error) {
		tb.Helper()
		_ = tx.Rollback()
		store.Close()
		tb.Fatal(err)
	}
	if _, err := tx.Exec(`insert into bundles(bundle_id,bundle_sha256,machine_id,captured_at,ingested_at,manifest_json) values('search-pathological-bundle','` + searchPathologicalHex(1) + `','bench-machine','2026-01-01T00:00:00Z','2026-01-01T00:00:01Z','{}')`); err != nil {
		fail(err)
	}
	sessionStmt, err := tx.Prepare(`insert into sessions(session_key,source_name,source_session_id,machine_id,raw_cwd,project_key,started_at) values(?,?,?,?,?,?,?)`)
	if err != nil {
		fail(err)
	}
	defer sessionStmt.Close()
	pathTokenStmt, err := tx.Prepare(`insert into session_path_tokens(session_key,token) values(?,?)`)
	if err != nil {
		fail(err)
	}
	defer pathTokenStmt.Close()
	entryStmt, err := tx.Prepare(`insert into entries(session_key,entry_id,line_no,entry_type,timestamp,role,entry_sha256,raw_json) values(?,?,?,?,?,?,?,?)`)
	if err != nil {
		fail(err)
	}
	defer entryStmt.Close()
	messageStmt, err := tx.Prepare(`insert into messages(session_key,entry_id,role,text) values(?,?,?,?)`)
	if err != nil {
		fail(err)
	}
	defer messageStmt.Close()
	perSession := 1000
	sessions := (messages + perSession - 1) / perSession
	for s := 0; s < sessions; s++ {
		sk := "sk1_" + searchPathologicalHex(1000+s)
		cwd := fmt.Sprintf("/bench/common/project-%04d", s)
		if s == sessions-1 {
			cwd = "/bench/rare/pathological-target"
		}
		project := fmt.Sprintf("project-%04d", s)
		if s == sessions-1 {
			project = "pathological-target"
		}
		if _, err := sessionStmt.Exec(sk, "pi", fmt.Sprintf("search-pathological-session-%04d", s), "bench-machine", cwd, project, "2026-01-01T00:00:00Z"); err != nil {
			fail(err)
		}
		for _, token := range []string{"bench", "common", project} {
			if _, err := pathTokenStmt.Exec(sk, token); err != nil {
				fail(err)
			}
		}
		start, end := s*perSession, (s+1)*perSession
		if end > messages {
			end = messages
		}
		for i := start; i < end; i++ {
			entryID := fmt.Sprintf("entry-%08d", i)
			role := "user"
			if i%2 == 1 {
				role = "assistant"
			}
			if _, err := entryStmt.Exec(sk, entryID, i-start+1, "message", fmt.Sprintf("2026-01-01T00:%02d:%02dZ", (i/60)%60, i%60), role, searchPathologicalHex(100000+i), `{"type":"message"}`); err != nil {
				fail(err)
			}
			text := fmt.Sprintf("pathological broadterm common-token message %08d in session %04d", i, s)
			if _, err := messageStmt.Exec(sk, entryID, role, text); err != nil {
				fail(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		store.Close()
		tb.Fatal(err)
	}
	return store
}

func searchPathologicalScale(env string, fallback int) int {
	if value := os.Getenv(env); value != "" {
		if n, err := strconv.Atoi(value); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func searchPathologicalHex(n int) string { return fmt.Sprintf("%064x", n) }
