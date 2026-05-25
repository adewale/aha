package corpus

import (
	"strings"
	"testing"
)

func TestCrossMachineConflictPrefetchUsesIndexes(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.DB.Query(`explain query plan select e.entry_id,e.session_key,e.entry_sha256 from entries e indexed by idx_entries_session_entry_hash join sessions s indexed by idx_sessions_source_session on s.session_key=e.session_key where s.source_name=? and s.source_session_id=? and s.machine_id<>?`, "pi", "session", "machine")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	text := plan.String()
	for _, want := range []string{"idx_sessions_source_session", "idx_entries_session_entry_hash"} {
		if !strings.Contains(text, want) {
			t.Fatalf("cross-machine conflict plan missing %s:\n%s", want, text)
		}
	}
}
