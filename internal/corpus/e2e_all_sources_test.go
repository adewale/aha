package corpus_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/adapters"
	"github.com/adewale/aha/internal/archive"
	"github.com/adewale/aha/internal/config"
	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
	"github.com/adewale/aha/internal/testutil"
)

// TestEndToEndAllSources drives every supported coding agent through the full
// pipeline against the shared fake fixtures — discovery -> snapshot -> bundle
// -> ingest -> search -> read — and asserts each source's distinctive needle is
// both searchable and readable. OpenCode (SQLite, converted to JSONL during
// discovery) goes through the same path as the JSONL agents.
func TestEndToEndAllSources(t *testing.T) {
	// Keep the OpenCode JSONL export hermetic and out of the real user cache.
	t.Setenv("AHA_OPENCODE_EXPORT_DIR", t.TempDir())

	root := t.TempDir()
	fx := testutil.WriteAgentFixtures(t, root)

	cfg := config.Default()
	cfg.MachineID = "e2e"
	cfg.Sources = []model.SourceConfig{
		{Type: "pi", Root: fx.PiRoot, Enabled: true},
		{Type: "claude-code", Root: fx.ClaudeRoot, Enabled: true},
		{Type: "codex", Root: fx.CodexRoot, Enabled: true},
		{Type: "opencode", Root: fx.OpenCodeRoot, Enabled: true},
	}

	bundle, err := archive.Capture(t.Context(), cfg, adapters.Builtins(), archive.Options{CapturedAt: "2026-01-03T00:00:00Z", BundleID: "e2e"})
	if err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(root, "bundle.tar.zst")
	if _, err := archive.Write(bundlePath, bundle); err != nil {
		t.Fatal(err)
	}
	store, err := corpus.Open(filepath.Join(root, "corpus"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := corpus.IngestBundle(store, adapters.Builtins(), bundlePath); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		source string
		needle string
	}{
		{"pi", "hello needle"},
		{"claude-code", "claude needle"},
		{"codex", "codex needle"},
		{"opencode", "opencode needle"},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			results, err := search.Query(store.DB, tc.needle, search.Filters{Source: tc.source, Limit: 5})
			if err != nil {
				t.Fatalf("search %q: %v", tc.needle, err)
			}
			if len(results) == 0 {
				t.Fatalf("no search hits for %s needle %q — source did not flow through the pipeline", tc.source, tc.needle)
			}
			r := results[0]
			if r.Source != tc.source {
				t.Fatalf("hit came from source %q, want %q", r.Source, tc.source)
			}
			// The ref returned by search must read back to real context.
			entries, err := corpus.ReadRef(store.DB, r.Ref, 0, 3)
			if err != nil {
				t.Fatalf("read %s: %v", r.RefText, err)
			}
			if !entryTextContains(entries, tc.needle) {
				t.Fatalf("read of %s did not surface needle %q; got %d entries", r.RefText, tc.needle, len(entries))
			}
		})
	}

	// OpenCode-specific richness: the converted assistant turn must preserve the
	// model, token usage, and the bash tool call through the whole pipeline.
	t.Run("opencode-metadata", func(t *testing.T) {
		results, err := search.Query(store.DB, "opencode reply", search.Filters{Source: "opencode", Limit: 5})
		if err != nil || len(results) == 0 {
			t.Fatalf("could not find opencode assistant turn: err=%v hits=%d", err, len(results))
		}
		entries, err := corpus.ReadRef(store.DB, results[0].Ref, 2, 2)
		if err != nil {
			t.Fatal(err)
		}
		if !entryRawContains(entries, "opencode-test") || !entryRawContains(entries, "bash") {
			t.Fatalf("opencode model/tool metadata lost in pipeline; entries=%+v", entries)
		}
	})
}

func entryTextContains(entries []corpus.ReadEntry, needle string) bool {
	for _, e := range entries {
		if strings.Contains(e.Text, needle) {
			return true
		}
	}
	return false
}

func entryRawContains(entries []corpus.ReadEntry, needle string) bool {
	for _, e := range entries {
		if strings.Contains(e.RawJSON, needle) || strings.Contains(e.Text, needle) {
			return true
		}
	}
	return false
}
