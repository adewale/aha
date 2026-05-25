package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
	"github.com/adewale/aha/internal/search"
)

func TestRenderSearchGolden(t *testing.T) {
	sessionKey, err := model.NewSessionKey("claude-code", "work-mac", "abc123")
	if err != nil {
		t.Fatal(err)
	}
	entryID, err := model.NewEntryID("entry-7")
	if err != nil {
		t.Fatal(err)
	}
	artifactSHA, err := model.ParseSHA256Hex("abcdef012345abcdef012345abcdef012345abcdef012345abcdef012345abcd")
	if err != nil {
		t.Fatal(err)
	}
	results := []search.Result{
		{
			Score:      0.125,
			Timestamp:  "2026-01-03T00:00:00Z",
			Source:     "claude-code",
			Machine:    "work-mac",
			Project:    "/Users/ade/projects/aha",
			Role:       "assistant",
			Snippet:    "fixed [needle] in parser",
			SessionKey: sessionKey.String(),
			EntryID:    entryID.String(),
			RefText:    model.FormatRef(model.MessageRef{Session: sessionKey, Entry: entryID}),
			Ref:        model.MessageRef{Session: sessionKey, Entry: entryID},
		},
		{
			Score:     0.25,
			Timestamp: "2026-01-04T00:00:00Z",
			Source:    "pi",
			Machine:   "work-mac",
			Project:   "notes.txt",
			Role:      "artifact",
			Snippet:   "artifact [needle] tail",
			EntryID:   artifactSHA.String(),
			RefText:   model.FormatRef(model.ArtifactRef{SHA: artifactSHA}),
			Ref:       model.ArtifactRef{SHA: artifactSHA},
		},
	}
	cases := map[string]renderMode{
		"search_human.golden": renderHuman,
		"search_json.golden":  renderJSON,
		"search_refs.golden":  renderRefs,
		"search_files.golden": renderFiles,
		"search_md.golden":    renderMD,
	}
	for name, mode := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := renderSearchResults(&out, results, mode); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, name, out.Bytes())
		})
	}
}

func TestRenderReadGolden(t *testing.T) {
	entries := []corpus.ReadEntry{
		{LineNo: 7, EntryID: "entry-7", Timestamp: "2026-01-03T00:00:00Z", Role: "assistant", Text: "fixed needle in parser", RawJSON: `{"role":"assistant"}`},
		{LineNo: 8, EntryID: "entry-8", Timestamp: "2026-01-03T00:00:01Z", Role: "tool", RawJSON: `{"tool":"go test"}`},
	}
	cases := map[string]renderMode{
		"read_human.golden": renderHuman,
		"read_json.golden":  renderJSON,
		"read_md.golden":    renderMD,
	}
	for name, mode := range cases {
		t.Run(name, func(t *testing.T) {
			var out bytes.Buffer
			if err := renderReadEntries(&out, entries, mode); err != nil {
				t.Fatal(err)
			}
			assertGolden(t, name, out.Bytes())
		})
	}
}

func assertGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if os.Getenv("AHA_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("golden mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}
