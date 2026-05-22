package corpus

import (
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/search"
)

func TestSearchDoesNotSeePausedUncommittedIngest(t *testing.T) {
	root := t.TempDir()
	bundlePath := writeFailureBundle(t, filepath.Join(root, "bundle-src"), "paused-ingest")
	corpusDir := filepath.Join(root, "corpus")
	writer, err := Open(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	reader, err := Open(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()

	entered := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	ing := Ingestor{Store: writer, Registry: adapters.Builtins(), hooks: ingestHooks{afterManifestFiles: func() error {
		close(entered)
		<-release
		return nil
	}}}
	go func() {
		_, err := ing.IngestBundle(bundlePath)
		done <- err
	}()

	<-entered
	results, err := search.Query(reader.DB, "needle", search.Filters{Limit: 10})
	if err != nil {
		close(release)
		t.Fatal(err)
	}
	if len(results) != 0 {
		close(release)
		t.Fatalf("reader saw uncommitted ingest results: %+v", results)
	}
	if _, err := ReadContext(reader.DB, "pi-session", "p1", 0, 0); err == nil {
		close(release)
		t.Fatalf("reader read uncommitted ingest session")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	entries, err := ReadContext(reader.DB, "pi-session", "p1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].EntryID != "p1" {
		t.Fatalf("reader did not read committed ingest entry: %+v", entries)
	}
	results, err = search.Query(reader.DB, "needle", search.Filters{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatalf("reader did not see committed ingest")
	}
}
