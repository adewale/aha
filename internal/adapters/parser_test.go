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
