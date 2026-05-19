package adapters

import (
	"strings"
	"testing"

	"github.com/adewale/aha/internal/model"
)

func FuzzParseGenericJSONL(f *testing.F) {
	f.Add(`{"type":"user","message":{"content":"hello"}}`)
	f.Add(`not json`)
	f.Add(`{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`)
	f.Fuzz(func(t *testing.T, input string) {
		_, err := parseGenericJSONL("claude-code", model.SessionFile{Source: "claude-code", SessionID: "s"}, strings.NewReader(input))
		if err != nil {
			t.Fatalf("parser returned scanner error: %v", err)
		}
	})
}

func TestParsePiNestedMessageRole(t *testing.T) {
	input := `{"type":"message","id":"p1","timestamp":"2026-01-01T00:00:01Z","message":{"role":"user","content":[{"type":"text","text":"real pi text"}]}}
{"type":"message","id":"p2","timestamp":"2026-01-01T00:00:02Z","message":{"role":"toolResult","toolName":"bash","content":[{"type":"text","text":"tool output"}]}}
`
	ps, err := parseGenericJSONL("pi", model.SessionFile{Source: "pi", SessionID: "s"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Entries) != 2 {
		t.Fatalf("entries=%d", len(ps.Entries))
	}
	if ps.Entries[0].Role != "user" || ps.Entries[0].Text != "real pi text" {
		t.Fatalf("bad user entry: %+v", ps.Entries[0])
	}
	if ps.Entries[1].Role != "toolResult" || ps.Entries[1].ToolName != "bash" {
		t.Fatalf("bad tool entry: %+v", ps.Entries[1])
	}
}

func TestParseImageDimensions(t *testing.T) {
	input := `{"type":"user","message":{"content":[{"type":"image","source":{"media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAAAAAA6fptVAAAACklEQVR42mP8z8AABQMBgAAn5B6iAAAAAElFTkSuQmCC"}}]}}`
	ps, err := parseGenericJSONL("claude-code", model.SessionFile{Source: "claude-code", SessionID: "s"}, strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Entries) != 1 || len(ps.Entries[0].Assets) != 1 {
		t.Fatalf("missing image asset: %+v", ps)
	}
	a := ps.Entries[0].Assets[0]
	if a.Width != 1 || a.Height != 1 || a.MimeType != "image/png" {
		t.Fatalf("bad image asset: %+v", a)
	}
}
