// Package mcp implements a read-only stdio MCP server over the aha corpus.
//
// Wire format: JSON-RPC 2.0 over stdio, with messages delimited by newlines.
// Per the MCP spec (https://modelcontextprotocol.io/specification/2025-06-18/basic/transports):
//
//	"Messages are delimited by newlines, and MUST NOT contain embedded newlines."
//
// Stdout is reserved for protocol frames; diagnostics go to stderr. See
// docs/mcp-spec.md.
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const (
	jsonRPCVersion  = "2.0"
	protocolVersion = "2024-11-05"
	serverName      = "aha"

	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeToolError      = -32000
)

// Message is a decoded JSON-RPC envelope.
type Message struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func newResult(id json.RawMessage, result any) *response {
	return &response{JSONRPC: jsonRPCVersion, ID: id, Result: result}
}

func newError(id json.RawMessage, code int, message string) *response {
	return &response{JSONRPC: jsonRPCVersion, ID: id, Error: &responseError{Code: code, Message: message}}
}

// EncodeFrame returns a JSON-RPC frame ready for the wire. Exported for tests
// and any host that wants to drive the server in-process.
//
// Output is a compact JSON encoding of v followed by a single '\n'.
// encoding/json never emits literal newlines inside its output, so the
// embedded-newline prohibition holds by construction.
func EncodeFrame(v any) ([]byte, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if bytes.ContainsAny(body, "\r\n") {
		// Defence-in-depth: should never happen with json.Marshal, but if a
		// future change introduces newlines (e.g. SetIndent) the wire format
		// silently breaks. Better to fail loudly here.
		return nil, fmt.Errorf("encoded JSON contains a newline: %q", body)
	}
	return append(body, '\n'), nil
}

// ParseFrames pulls all complete newline-delimited JSON messages out of buf
// and returns the remainder. A partial trailing message (no terminating
// newline yet) stays in rest unchanged.
//
// Input handling is intentionally lenient:
//   - both '\n' and '\r\n' line endings are accepted as delimiters
//   - empty / whitespace-only lines are skipped (clients sometimes pad)
//
// Spec compliance is enforced strictly on output via EncodeFrame.
func ParseFrames(buf []byte) (msgs []Message, rest []byte, err error) {
	rest = buf
	for {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			return msgs, rest, nil
		}
		line := rest[:nl]
		// Strip a trailing \r so \r\n line endings parse the same as \n.
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		line = bytes.TrimSpace(line)
		// Advance past the newline regardless of whether the line decoded.
		rest = rest[nl+1:]
		if len(line) == 0 {
			continue
		}
		var msg Message
		if jerr := json.Unmarshal(line, &msg); jerr != nil {
			return msgs, rest, jerr
		}
		msgs = append(msgs, msg)
	}
}
