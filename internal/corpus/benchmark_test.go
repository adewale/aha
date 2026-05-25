package corpus_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/hash"
	"github.com/adewale/aha/internal/model"
)

func BenchmarkIngestBundle(b *testing.B) {
	path := writeCorpusBenchBundle(b, b.TempDir(), 10, 100)
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(info.Size())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		store, err := corpus.Open(filepath.Join(b.TempDir(), fmt.Sprintf("corpus-%d", i)))
		if err != nil {
			b.Fatal(err)
		}
		if _, err := corpus.IngestBundle(store, adapters.Builtins(), path); err != nil {
			store.Close()
			b.Fatal(err)
		}
		store.Close()
	}
}

func BenchmarkVerify(b *testing.B) {
	path := writeCorpusBenchBundle(b, b.TempDir(), 20, 100)
	store, err := corpus.Open(filepath.Join(b.TempDir(), "corpus"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := corpus.Verify(store); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkReconcileFTS(b *testing.B) {
	path := writeCorpusBenchBundle(b, b.TempDir(), 20, 100)
	store, err := corpus.Open(filepath.Join(b.TempDir(), "corpus"))
	if err != nil {
		b.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), path); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := corpus.ReconcileFTS(store); err != nil {
			b.Fatal(err)
		}
	}
}

func writeCorpusBenchBundle(tb testing.TB, root string, sessions, entriesPerSession int) string {
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
	manifest := model.Manifest{Schema: model.BundleSchema, BundleID: "corpus-bench", MachineID: "bench-machine", CapturedAt: "2026-01-01T00:00:00Z", Policy: model.ManifestPolicy{PathMode: "raw", IncludeSubagents: true, IncludeImages: true, Redaction: "none-v1"}, Counts: model.ManifestCounts{SessionFiles: sessions, BytesUncompressed: total}, Files: manifestFiles}
	path := filepath.Join(root, "bench.tar.zst")
	if _, err := archive.Write(path, archive.Bundle{Manifest: manifest, Files: captured}); err != nil {
		tb.Fatal(err)
	}
	return path
}
