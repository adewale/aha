package corpus_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/corpus"
)

func BenchmarkPathologicalIngestManyTinyEntries(b *testing.B) {
	cases := []int{10000, pathologicalScale("AHA_PATHOLOGICAL_INGEST_ENTRIES", 10000)}
	if v := os.Getenv("AHA_PATHOLOGICAL_INGEST_LARGE"); v != "" {
		cases = append(cases, pathologicalScale("AHA_PATHOLOGICAL_INGEST_LARGE", 50000), pathologicalScale("AHA_PATHOLOGICAL_INGEST_XL", 100000))
	}
	seen := map[int]bool{}
	for _, entries := range cases {
		if seen[entries] {
			continue
		}
		seen[entries] = true
		b.Run(fmt.Sprintf("entries_%d", entries), func(b *testing.B) {
			sessions := 20
			if entries < sessions {
				sessions = entries
			}
			entriesPerSession := (entries + sessions - 1) / sessions
			path := writeCorpusBenchBundle(b, b.TempDir(), sessions, entriesPerSession)
			info, err := os.Stat(path)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.SetBytes(info.Size())
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				store, err := corpus.Open(filepath.Join(b.TempDir(), fmt.Sprintf("corpus-pathological-ingest-%d", i)))
				if err != nil {
					b.Fatal(err)
				}
				if _, err := corpus.IngestBundle(store, adapters.Builtins(), path); err != nil {
					store.Close()
					b.Fatal(err)
				}
				store.Close()
			}
		})
	}
}

func BenchmarkPathologicalVerifyFTSJoinScaling(b *testing.B) {
	for _, messages := range []int{1000, 2000, pathologicalScale("AHA_PATHOLOGICAL_VERIFY_MESSAGES", 5000)} {
		b.Run(fmt.Sprintf("messages_%d", messages), func(b *testing.B) {
			store := seedPathologicalCorpus(b, messages, messages/10)
			defer store.Close()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := corpus.Verify(store); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkPathologicalReconcileFTSRebuild(b *testing.B) {
	messages := pathologicalScale("AHA_PATHOLOGICAL_RECONCILE_MESSAGES", 5000)
	store := seedPathologicalCorpus(b, messages, messages/10)
	defer store.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := corpus.ReconcileFTS(store); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPathologicalStatusAndSnapshotSHAs(b *testing.B) {
	messages := pathologicalScale("AHA_PATHOLOGICAL_STATUS_MESSAGES", 5000)
	store := seedPathologicalCorpus(b, messages, messages/10)
	defer store.Close()
	seedExtraBundles(b, store, pathologicalScale("AHA_PATHOLOGICAL_STATUS_BUNDLES", 5000))
	b.Run("status-counts", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := corpus.Status(store.DB, store.Root); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("bundle-shas", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			if _, err := corpus.SnapshotManifestSHAs(store.DB); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func seedPathologicalCorpus(tb testing.TB, messages, artifacts int) *corpus.Store {
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
	if _, err := tx.Exec(`insert into snapshots(manifest_sha256,machine_id,captured_at,ingested_at,manifest_json) values('` + pathologicalHex(1) + `','bench-machine','2026-01-01T00:00:00Z','2026-01-01T00:00:01Z','{}')`); err != nil {
		fail(err)
	}
	sessionStmt, err := tx.Prepare(`insert into sessions(session_key,source_name,source_session_id,machine_id,raw_cwd,started_at) values(?,?,?,?,?,?)`)
	if err != nil {
		fail(err)
	}
	defer sessionStmt.Close()
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
		sk := "sk1_" + pathologicalHex(1000+s)
		cwd := fmt.Sprintf("/bench/common/project-%04d", s)
		if s == sessions-1 {
			cwd = "/bench/rare/pathological-target"
		}
		if _, err := sessionStmt.Exec(sk, "pi", fmt.Sprintf("pathological-session-%04d", s), "bench-machine", cwd, "2026-01-01T00:00:00Z"); err != nil {
			fail(err)
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
			if _, err := entryStmt.Exec(sk, entryID, i-start+1, "message", fmt.Sprintf("2026-01-01T00:%02d:%02dZ", (i/60)%60, i%60), role, pathologicalHex(100000+i), `{"type":"message"}`); err != nil {
				fail(err)
			}
			text := fmt.Sprintf("pathological needle common-token message %08d in session %04d", i, s)
			if _, err := messageStmt.Exec(sk, entryID, role, text); err != nil {
				fail(err)
			}
		}
	}
	artifactStmt, err := tx.Prepare(`insert into artifacts(artifact_sha256,source_name,machine_id,manifest_sha256,kind,raw_path,relative_path,text_preview,text_body) values(?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		fail(err)
	}
	defer artifactStmt.Close()
	for i := 0; i < artifacts; i++ {
		body := fmt.Sprintf("pathological needle artifact body %08d", i)
		if _, err := artifactStmt.Exec(pathologicalHex(200000+i), "pi", "bench-machine", "pathological-bundle", "artifact", fmt.Sprintf("/bench/artifacts/%08d.md", i), fmt.Sprintf("artifacts/%08d.md", i), body, body); err != nil {
			fail(err)
		}
	}
	if err := tx.Commit(); err != nil {
		store.Close()
		tb.Fatal(err)
	}
	return store
}

func seedExtraBundles(tb testing.TB, store *corpus.Store, bundles int) {
	tb.Helper()
	tx, err := store.DB.Begin()
	if err != nil {
		tb.Fatal(err)
	}
	stmt, err := tx.Prepare(`insert or ignore into snapshots(manifest_sha256,machine_id,captured_at,ingested_at,manifest_json) values(?,?,?,?,?)`)
	if err != nil {
		_ = tx.Rollback()
		tb.Fatal(err)
	}
	defer stmt.Close()
	for i := 0; i < bundles; i++ {
		if _, err := stmt.Exec(fmt.Sprintf("extra-bundle-%08d", i), pathologicalHex(900000+i), "bench-machine", "2026-01-01T00:00:00Z", "2026-01-01T00:00:01Z", "{}"); err != nil {
			_ = tx.Rollback()
			tb.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		tb.Fatal(err)
	}
}

func pathologicalScale(env string, fallback int) int {
	if value := os.Getenv(env); value != "" {
		if n, err := strconv.Atoi(value); err == nil && n > 0 {
			return n
		}
	}
	return fallback
}

func pathologicalHex(n int) string {
	return fmt.Sprintf("%064x", n)
}
