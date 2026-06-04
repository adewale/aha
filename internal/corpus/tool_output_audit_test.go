package corpus_test

import (
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/corpus"
)

func TestIndexToolOutputFalseGatesAnthropicUserToolResultBlocks(t *testing.T) {
	lines := []string{
		`{"type":"session","version":3,"id":"tool-gate","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"tr1","timestamp":"2026-01-01T00:00:01Z","role":"user","message":{"content":[{"type":"tool_result","content":"unique-tool-output-token"}]}}`,
	}
	store, err := corpus.Open(filepath.Join(t.TempDir(), "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := ingestPiBundleWithRedactor(t, store, "tool-gate", "tool-gate", lines, nil, ""); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := store.DB.QueryRow(`select count(*) from fts_messages where text match 'unique'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("tool_result content reached FTS despite index_tool_output=false: %d rows", n)
	}
}
