package corpus_test

import (
	"testing"

	"github.com/adewale/aha/internal/corpus"
	"github.com/adewale/aha/internal/model"
)

// TestMessagesPopulatesStateTransitionColumns pins priority 4 from
// docs/research/cross-agent-data-capture.md: Pi state-transition
// entries land in typed columns rather than being reachable only by
// LIKE against raw_json.
//
//	model_change           → messages.model + messages.provider
//	thinking_level_change  → messages.thinking_level
//	label                  → messages.label + messages.label_target_entry_id
func TestMessagesPopulatesStateTransitionColumns(t *testing.T) {
	store, _ := corpusWithStateTransitions(t)
	defer store.Close()

	// model_change
	var model_, provider string
	err := store.DB.QueryRow(`select coalesce(model,''), coalesce(provider,'') from messages where entry_id='mc1'`).Scan(&model_, &provider)
	if err != nil {
		t.Fatalf("model_change scan: %v", err)
	}
	if model_ != "claude-sonnet-4-6" || provider != "anthropic" {
		t.Fatalf("model_change populated model=%q provider=%q; want model=claude-sonnet-4-6 provider=anthropic", model_, provider)
	}

	// thinking_level_change
	var level string
	err = store.DB.QueryRow(`select coalesce(thinking_level,'') from messages where entry_id='tlc1'`).Scan(&level)
	if err != nil {
		t.Fatalf("thinking_level_change scan: %v", err)
	}
	if level != "high" {
		t.Fatalf("thinking_level_change populated thinking_level=%q; want 'high'", level)
	}

	// label
	var label, target string
	err = store.DB.QueryRow(`select coalesce(label,''), coalesce(label_target_entry_id,'') from messages where entry_id='lbl1'`).Scan(&label, &target)
	if err != nil {
		t.Fatalf("label scan: %v", err)
	}
	if label != "investigate-later" || target != "u1" {
		t.Fatalf("label populated label=%q target=%q; want label=investigate-later target=u1", label, target)
	}
}

func corpusWithStateTransitions(t *testing.T) (*corpus.Store, model.SessionKey) {
	t.Helper()
	lines := []string{
		`{"type":"session","version":3,"id":"states","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}`,
		`{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"model_change","id":"mc1","parentId":"u1","timestamp":"2026-01-01T00:00:02Z","provider":"anthropic","modelId":"claude-sonnet-4-6"}`,
		`{"type":"thinking_level_change","id":"tlc1","parentId":"mc1","timestamp":"2026-01-01T00:00:03Z","thinkingLevel":"high"}`,
		`{"type":"label","id":"lbl1","parentId":"tlc1","timestamp":"2026-01-01T00:00:04Z","targetId":"u1","label":"investigate-later"}`,
		`{"type":"message","id":"a1","parentId":"lbl1","timestamp":"2026-01-01T00:00:05Z","message":{"role":"assistant","content":[{"type":"text","text":"ok"}]}}`,
	}
	return ingestPiLines(t, "states", lines)
}
