package mcp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestEncodeFrameProducesNewlineDelimitedJSON(t *testing.T) {
	msg := Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "ping"}
	frame, err := EncodeFrame(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(frame) == 0 || frame[len(frame)-1] != '\n' {
		t.Fatalf("frame does not end with newline: %q", frame)
	}
	// Body before the newline must be valid compact JSON with no embedded newline.
	body := frame[:len(frame)-1]
	if bytes.ContainsAny(body, "\r\n") {
		t.Fatalf("body contains an embedded newline (spec violation): %q", body)
	}
	if !json.Valid(body) {
		t.Fatalf("body is not valid JSON: %q", body)
	}
	// And: must not start with the Content-Length header from the legacy format.
	if bytes.HasPrefix(frame, []byte("Content-Length:")) {
		t.Fatalf("frame uses legacy Content-Length framing: %q", frame)
	}
}

func TestEncodeParseRoundTrip(t *testing.T) {
	msg := Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "ping"}
	frame, err := EncodeFrame(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, rest, err := ParseFrames(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("trailing bytes after parse: %q", rest)
	}
	if len(got) != 1 || got[0].Method != "ping" {
		t.Fatalf("got %+v, want method=ping", got)
	}
}

func TestParseFramesPartialBufferIsRetained(t *testing.T) {
	frame, err := EncodeFrame(Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "ping"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	// Cut the frame before the trailing newline; the parser must not yield
	// a message until the line terminator arrives.
	cut := len(frame) - 1
	msgs, rest, perr := ParseFrames(frame[:cut])
	if perr != nil {
		t.Fatalf("parse partial: %v", perr)
	}
	if len(msgs) != 0 {
		t.Fatalf("got msgs=%d, want 0 on partial input", len(msgs))
	}
	if !bytes.Equal(rest, frame[:cut]) {
		t.Fatalf("partial frame not preserved: got %q want %q", rest, frame[:cut])
	}
	// Append the trailing newline and parse again.
	rest = append(rest, frame[cut:]...)
	msgs, rest, perr = ParseFrames(rest)
	if perr != nil {
		t.Fatalf("parse completed: %v", perr)
	}
	if len(msgs) != 1 || msgs[0].Method != "ping" {
		t.Fatalf("got %+v after completion", msgs)
	}
	if len(rest) != 0 {
		t.Fatalf("trailing bytes after completion: %q", rest)
	}
}

func TestParseFramesTwoMessages(t *testing.T) {
	a, _ := EncodeFrame(Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "ping"})
	b, _ := EncodeFrame(Message{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"})
	combined := append([]byte{}, a...)
	combined = append(combined, b...)
	msgs, rest, err := ParseFrames(combined)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rest) != 0 {
		t.Fatalf("rest non-empty: %q", rest)
	}
	if len(msgs) != 2 || msgs[0].Method != "ping" || msgs[1].Method != "tools/list" {
		t.Fatalf("got %+v", msgs)
	}
}

// Real MCP clients sometimes emit \r\n line endings (Windows-flavored
// pipes, network bridges); the parser must accept them transparently.
func TestParseFramesAcceptsCRLF(t *testing.T) {
	frame := []byte(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\r\n")
	msgs, rest, err := ParseFrames(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rest) != 0 || len(msgs) != 1 || msgs[0].Method != "ping" {
		t.Fatalf("got msgs=%+v rest=%q", msgs, rest)
	}
}

// Blank padding lines must be skipped rather than treated as parse errors;
// some clients insert empty lines as keep-alives.
func TestParseFramesSkipsBlankLines(t *testing.T) {
	frame := []byte("\n\n" + `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n\n")
	msgs, rest, err := ParseFrames(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rest) != 0 || len(msgs) != 1 {
		t.Fatalf("got msgs=%d rest=%q", len(msgs), rest)
	}
}

func TestParseFramesReportsMalformedJSON(t *testing.T) {
	frame := []byte("not json\n")
	_, _, err := ParseFrames(frame)
	if err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("expected JSON parse error, got %v", err)
	}
}

// (json.Marshal never emits literal newlines in Go-encoded values — control
// characters in strings escape to "\\n" — so EncodeFrame's belt-and-braces
// newline check is impossible to trigger from a normal call site. The check
// exists in production as defence against a future SetIndent / custom
// MarshalJSON regression, but a unit test for it would have to call
// json.Marshal manually and bypass the public API.)
