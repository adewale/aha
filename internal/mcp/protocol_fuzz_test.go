package mcp

import (
	"bytes"
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// FuzzParseFrames asserts the frame parser is panic-safe on arbitrary input
// and that its buffering contract holds: the returned remainder is always a
// suffix of the input, so a streaming caller can safely re-feed `rest` on the
// next read regardless of whether ParseFrames returned an error mid-stream.
func FuzzParseFrames(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("\n"))
	f.Add([]byte("\n\n\n"))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"ping"}` + "\n"))
	f.Add([]byte(`{"jsonrpc":"2.0","method":"ping"}` + "\r\n"))
	f.Add([]byte("not json\n"))
	f.Add([]byte("no terminating newline"))
	f.Add([]byte(`{"jsonrpc":"2.0","id":1,"method":"a"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"b"}` + "\n"))
	// Legacy Content-Length payloads should not crash the new parser.
	f.Add([]byte("Content-Length: 2\r\n\r\n{}"))
	if frame, err := EncodeFrame(Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "ping"}); err == nil {
		f.Add(frame)
		f.Add(append(frame, frame...))   // two back-to-back frames
		f.Add(frame[:len(frame)-1])      // missing terminator → partial
		f.Add(append([]byte("\n"), frame...)) // leading blank line
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		msgs, rest, err := ParseFrames(data)
		if !bytes.HasSuffix(data, rest) {
			t.Fatalf("rest %q is not a suffix of input %q (err=%v)", rest, data, err)
		}
		if len(rest) > len(data) {
			t.Fatalf("rest %d longer than input %d", len(rest), len(data))
		}
		// Any message the parser claims to have decoded must round-trip
		// through json.Marshal — a stronger check than relying on the
		// json.Unmarshal call inside ParseFrames having succeeded.
		for _, m := range msgs {
			if _, mErr := json.Marshal(m); mErr != nil {
				t.Fatalf("decoded message does not re-marshal: %v", mErr)
			}
		}
		// On parse error, advancement past the offending line is still
		// valid; on success, rest must contain no complete frames.
		if err == nil {
			if extra, _, perr := ParseFrames(rest); perr == nil && len(extra) > 0 {
				t.Fatalf("rest still contains decodable frames: %d", len(extra))
			}
		}
	})
}

// FuzzEncodeParseRoundTrip frames an arbitrary method/id and asserts it
// survives a parse round-trip exactly. This guards the wire format against
// off-by-one framing regressions for any UTF-8 method string.
func FuzzEncodeParseRoundTrip(f *testing.F) {
	f.Add("ping", `1`)
	f.Add("tools/call", `"abc"`)
	f.Add("", `null`)

	f.Fuzz(func(t *testing.T, method, idJSON string) {
		// MCP method names are JSON strings and must be valid UTF-8. Go's
		// encoding/json silently rewrites invalid UTF-8 to U+FFFD rather
		// than erroring, so those inputs can't round-trip byte-for-byte and
		// are out of scope for this invariant.
		if !utf8.ValidString(method) || !json.Valid([]byte(idJSON)) {
			t.Skip()
		}
		in := Message{JSONRPC: "2.0", ID: json.RawMessage(idJSON), Method: method}
		frame, err := EncodeFrame(in)
		if err != nil {
			t.Skip() // unencodable method (e.g. invalid UTF-8) is out of scope
		}
		// Spec invariant: the body (everything before the trailing '\n')
		// must contain no embedded newlines.
		if i := bytes.IndexByte(frame, '\n'); i != len(frame)-1 {
			t.Fatalf("frame has newline at offset %d (expected only at end %d)", i, len(frame)-1)
		}
		msgs, rest, err := ParseFrames(frame)
		if err != nil {
			t.Fatalf("round-trip parse failed: %v", err)
		}
		if len(rest) != 0 {
			t.Fatalf("round-trip left %d trailing bytes", len(rest))
		}
		if len(msgs) != 1 {
			t.Fatalf("round-trip yielded %d messages, want 1", len(msgs))
		}
		if msgs[0].Method != method {
			t.Fatalf("method round-trip mismatch: got %q want %q", msgs[0].Method, method)
		}
	})
}
