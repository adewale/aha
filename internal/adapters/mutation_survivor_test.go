package adapters

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

// Tests in this file target specific surviving mutants flagged by
// `go run github.com/go-gremlins/gremlins/cmd/gremlins@v0.6.0 unleash
// ./internal/adapters --workers 2 --timeout-coefficient 6` so that
// downstream refactors of parser.go / pi.go don't quietly weaken the
// invariants those bits encode. Each test names the mutant it pins.

// piHeaderIDFromRaw line 79 (CONDITIONALS_NEGATION): the function
// must return "" when the input is not valid JSON, not when it IS.
func TestPiHeaderIDFromRawReturnsEmptyOnInvalidJSON(t *testing.T) {
	if id := piHeaderIDFromRaw([]byte("not json")); id != "" {
		t.Fatalf("piHeaderIDFromRaw(not json)=%q want \"\"", id)
	}
	if id := piHeaderIDFromRaw([]byte(`{"type":"session","id":"valid"}`)); id != "valid" {
		t.Fatalf("piHeaderIDFromRaw(valid session)=%q want \"valid\"", id)
	}
}

// piHeaderIDFromRaw line 82 (CONDITIONALS_NEGATION): the function
// must return "" when the entry type is not "session", not when it IS.
func TestPiHeaderIDFromRawReturnsEmptyForNonSessionType(t *testing.T) {
	if id := piHeaderIDFromRaw([]byte(`{"type":"message","id":"m1"}`)); id != "" {
		t.Fatalf("piHeaderIDFromRaw(non-session)=%q want \"\"", id)
	}
	if id := piHeaderIDFromRaw([]byte(`{"type":"session","id":"s1"}`)); id != "s1" {
		t.Fatalf("piHeaderIDFromRaw(session)=%q want \"s1\"", id)
	}
}

// Pi.ParseSession line 96 (CONDITIONALS_NEGATION): the header's id
// must overwrite SourceSessionID only when the header id is non-empty.
// If the SessionFile already has a SessionID and the header carries
// the same one, the SourceSessionID must match the header value (not
// fall back to file-path-derived id).
func TestPiParseSessionPrefersHeaderID(t *testing.T) {
	input := `{"type":"session","version":3,"id":"from-header","timestamp":"2026-01-01T00:00:00Z","cwd":"/tmp"}
{"type":"message","id":"u1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
`
	ps, err := Pi{}.ParseSession(t.Context(), model.SessionFile{Source: "pi", SessionID: "from-filename"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if ps.SourceSessionID != "from-header" {
		t.Fatalf("SourceSessionID=%q want \"from-header\" (header should win over filename)", ps.SourceSessionID)
	}
}

// parser.go line 69 (CONDITIONALS_NEGATION): the model lookup at the
// top-level should only overwrite pe.Model when it's still empty.
// If message.model is set, the top-level model must NOT clobber it.
func TestParseModelPrefersMessageModelOverTopLevel(t *testing.T) {
	const input = `{"type":"message","id":"e1","timestamp":"2026-01-01T00:00:01Z","model":"top-level-model","message":{"role":"assistant","content":[{"type":"text","text":"hi"}],"model":"message-model"}}`
	ps, err := parseGenericJSONL("claude-code", model.SessionFile{Source: "claude-code", SessionID: "s"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Entries) != 1 || ps.Entries[0].Model != "message-model" {
		t.Fatalf("Model=%q want \"message-model\" (top-level must not clobber message.model)", ps.Entries[0].Model)
	}
}
