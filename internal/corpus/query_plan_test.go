package corpus_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

// TestKnownSessionVersionLookupUsesIndex pins the query plan of the
// parse-skip lookup (invariant I7): it must be an index seek, not a scan.
// Without a covering index, a long-lived corpus pays O(observed versions
// of the file) per ingest skip — versions accumulate one per snapshot, so
// the skip itself would become a Shlemiel.
func TestKnownSessionVersionLookupUsesIndex(t *testing.T) {
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.DB.Query(`explain query plan
		select sv.session_key from session_versions sv
		join sessions s on s.session_key=sv.session_key
		where sv.file_sha256=? and sv.relative_path=? and sv.raw_path=? and s.machine_id=? and s.source_name=? limit 1`,
		strings.Repeat("a", 64), "rel", "raw", "m", "src")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if !strings.Contains(joined, "idx_session_versions_lookup") {
		t.Fatalf("known-version lookup does not seek idx_session_versions_lookup:\n%s", joined)
	}
	for _, line := range plan {
		if strings.HasPrefix(line, "SCAN") && strings.Contains(line, "session_versions") {
			t.Fatalf("known-version lookup scans session_versions:\n%s", joined)
		}
	}
}
