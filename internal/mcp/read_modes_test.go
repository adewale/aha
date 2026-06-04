package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/mcp"
)

// TestReadToolBranchMode pins that the MCP read tool — the agent-facing
// surface — can walk a Pi branch via mode:"branch", not just the
// file-order window. The fixture Pi session is p1 → p2; reading branch
// leaf p2 must return the leaf→root path [p1, p2].
func TestReadToolBranchMode(t *testing.T) {
	store, cfg := buildCorpus(t)
	backend := mcp.NewCorpusBackend(store, cfg)
	out, err := mcp.CallTool(backend, "read", json.RawMessage(`{"session":"pi-session","entry":"p2","mode":"branch"}`))
	if err != nil {
		t.Fatalf("CallTool(read branch): %v", err)
	}
	body, _ := json.Marshal(out)
	s := string(body)
	if !strings.Contains(s, `"p1"`) || !strings.Contains(s, `"p2"`) {
		t.Fatalf("branch read missing expected path entries: %s", s)
	}
	// Order: p1 before p2 (root → leaf).
	if strings.Index(s, `"p1"`) > strings.Index(s, `"p2"`) {
		t.Fatalf("branch read out of order (want p1 before p2): %s", s)
	}
}

// TestReadToolLiveMode pins that the MCP read tool can reconstruct the
// compaction-aware live context via mode:"live". The fixture session
// has no compaction, so live context equals the branch path — but the
// mode must be accepted and dispatched, not rejected as an unknown arg.
func TestReadToolLiveMode(t *testing.T) {
	store, cfg := buildCorpus(t)
	backend := mcp.NewCorpusBackend(store, cfg)
	out, err := mcp.CallTool(backend, "read", json.RawMessage(`{"session":"pi-session","entry":"p2","mode":"live"}`))
	if err != nil {
		t.Fatalf("CallTool(read live): %v", err)
	}
	body, _ := json.Marshal(out)
	if !strings.Contains(string(body), `"p2"`) {
		t.Fatalf("live read missing leaf entry: %s", body)
	}
}

// TestReadToolBranchModeRequiresEntry pins that branch/live modes need
// a leaf entry; without one the tool returns a clear error rather than
// silently falling back to a window read.
func TestReadToolWindowHonorsExplicitZeroContext(t *testing.T) {
	store, cfg := buildCorpus(t)
	backend := mcp.NewCorpusBackend(store, cfg)
	out, err := mcp.CallTool(backend, "read", json.RawMessage(`{"session":"pi-session","entry":"p2","before":0,"after":0}`))
	if err != nil {
		t.Fatalf("CallTool(read zero window): %v", err)
	}
	body, _ := json.Marshal(out)
	var entries []struct {
		EntryID string `json:"entry_id"`
	}
	if err := json.Unmarshal(body, &entries); err != nil {
		t.Fatalf("unexpected read output %T: %s", out, body)
	}
	if len(entries) != 1 || entries[0].EntryID != "p2" {
		t.Fatalf("explicit before=0 after=0 returned %+v, want only p2", entries)
	}
}

func TestReadToolBranchModeRequiresEntry(t *testing.T) {
	store, cfg := buildCorpus(t)
	backend := mcp.NewCorpusBackend(store, cfg)
	_, err := mcp.CallTool(backend, "read", json.RawMessage(`{"session":"pi-session","mode":"branch"}`))
	if err == nil {
		t.Fatal("expected error: branch mode without entry")
	}
}

// TestReadToolRejectsUnknownMode keeps the mode enum closed.
func TestReadToolRejectsUnknownMode(t *testing.T) {
	store, cfg := buildCorpus(t)
	backend := mcp.NewCorpusBackend(store, cfg)
	_, err := mcp.CallTool(backend, "read", json.RawMessage(`{"session":"pi-session","entry":"p2","mode":"sideways"}`))
	if err == nil {
		t.Fatal("expected error on unknown read mode")
	}
}
