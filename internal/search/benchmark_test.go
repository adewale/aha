package search_test

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
)

func BenchmarkQuery(b *testing.B) {
	store := buildSearchBenchCorpus(b, 20, 100)
	defer store.Close()
	benchmarks := []struct {
		name    string
		query   string
		filters search.Filters
	}{
		{name: "default", query: "bench needle", filters: search.Filters{Limit: 20}},
		{name: "source-filter", query: "bench needle", filters: search.Filters{Source: "pi", Limit: 20}},
		{name: "path-filter", query: "bench needle", filters: search.Filters{Path: "project-001", Limit: 20}},
		{name: "path-token-filter", query: "bench needle", filters: search.Filters{PathToken: "project-001", Limit: 20}},
		{name: "project-filter", query: "bench needle", filters: search.Filters{Project: "project-001", Limit: 20}},
		{name: "no-hit", query: "definitely-not-present", filters: search.Filters{Limit: 20}},
	}
	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := search.Query(store.DB, bm.query, bm.filters); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func buildSearchBenchCorpus(tb testing.TB, sessions, entriesPerSession int) *corpus.Store {
	tb.Helper()
	root := tb.TempDir()
	bundle := writeSearchBenchBundle(tb, root, sessions, entriesPerSession)
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		tb.Fatal(err)
	}
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundle); err != nil {
		store.Close()
		tb.Fatal(err)
	}
	return store
}

func writeSearchBenchBundle(tb testing.TB, root string, sessions, entriesPerSession int) string {
	tb.Helper()
	captured := make([]model.CapturedFile, 0, sessions)
	manifestFiles := make([]model.ManifestFile, 0, sessions)
	var total int64
	for s := 0; s < sessions; s++ {
		var lines strings.Builder
		fmt.Fprintf(&lines, `{"type":"session","id":"bench-session-%03d","timestamp":"2026-01-01T00:00:00Z","cwd":"/bench/project-%03d"}`+"\n", s, s)
		for e := 0; e < entriesPerSession; e++ {
			role := "user"
			if e%2 == 1 {
				role = "assistant"
			}
			fmt.Fprintf(&lines, `{"id":"e-%03d-%05d","type":"message","role":"%s","timestamp":"2026-01-01T00:%02d:%02dZ","message":{"content":"bench needle session %03d entry %05d"}}`+"\n", s, e, role, (e/60)%60, e%60, s, e)
		}
		data := []byte(lines.String())
		rel := fmt.Sprintf("sources/pi/sessions/bench-%03d.jsonl", s)
		mf := model.ManifestFile{Source: "pi", Kind: "session", RelativePath: rel, RawPath: rel, SHA256: hash.SHA256Bytes(data), Bytes: int64(len(data)), SessionID: fmt.Sprintf("bench-session-%03d", s), Entries: entriesPerSession, CopyState: "stable"}
		manifestFiles = append(manifestFiles, mf)
		captured = append(captured, model.CapturedFile{Manifest: mf, Data: data})
		total += int64(len(data))
	}
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "search-bench", MachineID: "bench-machine", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Counts: model.ManifestCounts{SessionFiles: sessions, BytesUncompressed: total}, Files: manifestFiles}
	path := filepath.Join(root, "bench.tar.zst")
	if _, err := archive.Write(path, archive.Bundle{Manifest: manifest, Files: captured}); err != nil {
		tb.Fatal(err)
	}
	return path
}
