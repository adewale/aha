package adapters

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func TestRawJSONPreservesSourceLineWhitespace(t *testing.T) {
	line := `  {"type":"message","id":"m1","role":"user","message":{"content":"kept"}}  `
	ps, err := parseGenericJSONL("pi", model.SessionFile{Source: "pi", SessionID: "s"}, strings.NewReader(line+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ps.Entries[0].RawJSON; got != line {
		t.Fatalf("RawJSON = %q, want exact source line %q", got, line)
	}
}

func TestGenericUserToolResultIsProjectedAsToolResult(t *testing.T) {
	input := `{"type":"message","id":"tr1","role":"user","message":{"content":[{"type":"tool_result","content":"tool output should be gated"}]}}`
	ps, err := parseGenericJSONL("claude-code", model.SessionFile{Source: "claude-code", SessionID: "s"}, strings.NewReader(input+"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if got := ps.Entries[0].Role; got != "toolResult" {
		t.Fatalf("role=%q want toolResult so index_tool_output can gate it", got)
	}
	if !strings.Contains(ps.Entries[0].Text, "tool output should be gated") {
		t.Fatalf("tool_result text was not extracted: %q", ps.Entries[0].Text)
	}
}

func TestCodexCommandArgumentsAcceptCmdAndObject(t *testing.T) {
	input := strings.Join([]string{
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","arguments":"{\"cmd\":[\"echo\",\"hi\"]}"}}`,
		`{"type":"response_item","payload":{"type":"function_call","name":"exec","arguments":{"cmd":"pwd"}}}`,
	}, "\n")
	ps, err := CodexCLI{}.ParseSession(t.Context(), model.SessionFile{Source: "codex", SessionID: "s"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if got := ps.Entries[0].Command; got != "echo hi" {
		t.Fatalf("string cmd command=%q want echo hi", got)
	}
	if got := ps.Entries[1].Command; got != "pwd" {
		t.Fatalf("object cmd command=%q want pwd", got)
	}
	if !strings.Contains(ps.Entries[1].FilesJSON, `"cmd":"pwd"`) {
		t.Fatalf("object arguments were not preserved as files_json: %q", ps.Entries[1].FilesJSON)
	}
}

func TestPiExcludeFromContextAndNativeImages(t *testing.T) {
	png := base64.StdEncoding.EncodeToString([]byte("not-a-real-png-but-stored"))
	input := strings.Join([]string{
		`{"type":"session","id":"pi-images","timestamp":"2026-01-01T00:00:00Z"}`,
		`{"type":"message","id":"hidden","message":{"role":"user","excludeFromContext":true,"content":"hidden"}}`,
		`{"type":"message","id":"img1","message":{"role":"user","content":[{"type":"image","mimeType":"image/png","data":"` + png + `"}]}}`,
		`{"type":"message","id":"img2","image":{"mimeType":"image/png","data":"` + png + `"},"message":{"role":"user","content":"see image"}}`,
	}, "\n")
	ps, err := Pi{}.ParseSession(t.Context(), model.SessionFile{Source: "pi", SessionID: "fallback"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if ps.Entries[0].ParticipatesInContext {
		t.Fatalf("excludeFromContext entry participates in context")
	}
	for _, idx := range []int{1, 2} {
		if len(ps.Entries[idx].Assets) != 1 {
			t.Fatalf("entry %d assets=%d want 1", idx, len(ps.Entries[idx].Assets))
		}
		if got := ps.Entries[idx].Assets[0].MimeType; got != "image/png" {
			t.Fatalf("entry %d mime=%q want image/png", idx, got)
		}
	}
}
