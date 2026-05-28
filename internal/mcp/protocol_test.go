package mcp

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEncodeParseRoundTrip(t *testing.T) {
	msg := Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "ping"}
	frame, err := EncodeFrame(msg)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !bytes.Contains(frame, []byte("Content-Length: ")) {
		t.Fatalf("frame missing Content-Length: %q", frame)
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
	// Cut the frame mid-body so the parser must hold the partial bytes.
	cut := len(frame) - 5
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
	// Append the rest and parse again.
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

func TestParseFramesRejectsInvalidContentLength(t *testing.T) {
	bad := []byte("Content-Length: not-a-number\r\n\r\n{}")
	_, _, err := ParseFrames(bad)
	if err == nil {
		t.Fatalf("expected error on invalid Content-Length")
	}
}

func TestContentLengthCaseInsensitive(t *testing.T) {
	frame := []byte("content-length: 2\r\n\r\n{}")
	msgs, rest, err := ParseFrames(frame)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rest) != 0 || len(msgs) != 1 {
		t.Fatalf("got msgs=%d rest=%q", len(msgs), rest)
	}
}
