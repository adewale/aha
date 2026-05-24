package adapters

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/adewale/aha/internal/model"
)

type adapterConformanceCase struct {
	Name        string            `json:"name"`
	Adapter     string            `json:"adapter"`
	Fixture     string            `json:"fixture"`
	SessionFile conformanceFile   `json:"session_file"`
	Want        conformanceExpect `json:"want"`
}

type conformanceFile struct {
	Source    string `json:"source"`
	SessionID string `json:"session_id"`
	CWD       string `json:"cwd"`
}

type conformanceExpect struct {
	Source            string            `json:"source"`
	SourceSessionID   string            `json:"source_session_id"`
	CWD               string            `json:"cwd"`
	StartedAt         string            `json:"started_at"`
	Entries           int               `json:"entries"`
	ContainsText      string            `json:"contains_text"`
	RolesByEntryID    map[string]string `json:"roles_by_entry_id"`
	ToolByEntryID     map[string]string `json:"tool_by_entry_id"`
	ParentByEntryID   map[string]string `json:"parent_by_entry_id"`
	RolesByIndex      map[string]string `json:"roles_by_index"`
	AssetCountByIndex map[string]int    `json:"asset_count_by_index"`
	ModelByIndex      map[string]string `json:"model_by_index"`
	TokensByIndex     map[string]int64  `json:"tokens_by_index"`
}

func TestAdapterConformanceFixtures(t *testing.T) {
	files, err := filepath.Glob("testdata/conformance/*.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no adapter conformance fixtures")
	}
	for _, path := range files {
		var tc adapterConformanceCase
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &tc); err != nil {
			t.Fatal(err)
		}
		t.Run(tc.Name, func(t *testing.T) {
			ad := Builtins()[tc.Adapter]
			if ad == nil {
				t.Fatalf("unknown adapter %q", tc.Adapter)
			}
			f, err := os.Open(tc.Fixture)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ps, err := ad.ParseSession(t.Context(), model.SessionFile{Source: tc.SessionFile.Source, SessionID: tc.SessionFile.SessionID, CWD: tc.SessionFile.CWD}, f)
			if err != nil {
				t.Fatal(err)
			}
			assertConformance(t, ps, tc.Want)
		})
	}
}

func assertConformance(t *testing.T, ps *model.ParsedSession, want conformanceExpect) {
	t.Helper()
	if ps.Source != want.Source || ps.SourceSessionID != want.SourceSessionID || len(ps.Entries) != want.Entries {
		t.Fatalf("parsed session source=%q id=%q entries=%d want source=%q id=%q entries=%d", ps.Source, ps.SourceSessionID, len(ps.Entries), want.Source, want.SourceSessionID, want.Entries)
	}
	if want.CWD != "" && ps.CWD != want.CWD {
		t.Fatalf("cwd=%q want %q", ps.CWD, want.CWD)
	}
	if want.StartedAt != "" && ps.StartedAt != want.StartedAt {
		t.Fatalf("started_at=%q want %q", ps.StartedAt, want.StartedAt)
	}
	if want.ContainsText != "" {
		found := false
		for _, entry := range ps.Entries {
			if entry.Text == want.ContainsText {
				found = true
			}
		}
		if !found {
			t.Fatalf("text %q not found in %+v", want.ContainsText, ps.Entries)
		}
	}
	byID := map[string]model.ParsedEntry{}
	for _, entry := range ps.Entries {
		byID[entry.EntryID] = entry
	}
	for id, role := range want.RolesByEntryID {
		if byID[id].Role != role {
			t.Fatalf("entry %s role=%q want %q", id, byID[id].Role, role)
		}
	}
	for id, tool := range want.ToolByEntryID {
		if byID[id].ToolName != tool {
			t.Fatalf("entry %s tool=%q want %q", id, byID[id].ToolName, tool)
		}
	}
	for id, parent := range want.ParentByEntryID {
		if byID[id].ParentID != parent {
			t.Fatalf("entry %s parent=%q want %q", id, byID[id].ParentID, parent)
		}
	}
	for idx, role := range want.RolesByIndex {
		entry := entryAt(t, ps, idx)
		if entry.Role != role {
			t.Fatalf("entry[%s] role=%q want %q", idx, entry.Role, role)
		}
	}
	for idx, count := range want.AssetCountByIndex {
		entry := entryAt(t, ps, idx)
		if len(entry.Assets) != count {
			t.Fatalf("entry[%s] asset count=%d want %d", idx, len(entry.Assets), count)
		}
	}
	for idx, modelName := range want.ModelByIndex {
		entry := entryAt(t, ps, idx)
		if entry.Model != modelName {
			t.Fatalf("entry[%s] model=%q want %q", idx, entry.Model, modelName)
		}
	}
	for idx, tokens := range want.TokensByIndex {
		entry := entryAt(t, ps, idx)
		if entry.Tokens != tokens {
			t.Fatalf("entry[%s] tokens=%d want %d", idx, entry.Tokens, tokens)
		}
	}
}

func entryAt(t *testing.T, ps *model.ParsedSession, idx string) model.ParsedEntry {
	t.Helper()
	i, err := strconv.Atoi(idx)
	if err != nil {
		t.Fatal(err)
	}
	if i < 0 || i >= len(ps.Entries) {
		t.Fatalf("entry index %d outside %d entries", i, len(ps.Entries))
	}
	return ps.Entries[i]
}
