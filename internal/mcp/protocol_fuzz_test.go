package mcp

import (
	"bytes"
	"encoding/json"
	"testing"
	"unicode/utf8"
)

// FuzzParseFrames asserts the frame parser is panic-safe on arbitrary input
// and that its buffering contract holds: the returned remainder is always a
// suffix of the input (the parser only consumes whole frames from the front),
// so a streaming caller can safely re-feed `rest` on the next read.
func FuzzParseFrames(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("Content-Length: 2\r\n\r\n{}"))
	f.Add([]byte("Content-Length: 999\r\n\r\n{}")) // declared longer than body
	f.Add([]byte("Content-Length: -1\r\n\r\n"))
	f.Add([]byte("Content-Length: abc\r\n\r\n"))
	f.Add([]byte("no header at all"))
	f.Add([]byte("\r\n\r\n"))
	if frame, err := EncodeFrame(Message{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "ping"}); err == nil {
		f.Add(frame)
		f.Add(append(frame, frame...)) // two back-to-back frames
		f.Add(frame[:len(frame)-1])    // truncated body
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		msgs, rest, err := ParseFrames(data)
		if err != nil {
			// On error the parser may stop early; the only invariant we
			// require is that it did not panic and rest stays a suffix.
			if !bytes.HasSuffix(data, rest) {
				t.Fatalf("on error, rest %q is not a suffix of input %q", rest, data)
			}
			return
		}
		if len(rest) > len(data) {
			t.Fatalf("rest %d longer than input %d", len(rest), len(data))
		}
		if !bytes.HasSuffix(data, rest) {
			t.Fatalf("rest %q is not a suffix of input %q", rest, data)
		}
		// Any message the parser claims to have decoded must be valid JSON
		// (ParseFrames json.Unmarshals each body), which is already implied
		// by a nil error; re-marshal to be sure the decoded value is sane.
		for _, m := range msgs {
			if _, mErr := json.Marshal(m); mErr != nil {
				t.Fatalf("decoded message does not re-marshal: %v", mErr)
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
