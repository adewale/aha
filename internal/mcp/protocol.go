// Package mcp implements a read-only stdio MCP server over the aha corpus.
//
// Wire format: JSON-RPC 2.0 framed with LSP-style "Content-Length:" headers.
// Transport: stdio. Stdout is reserved for protocol frames; diagnostics go to
// stderr. See docs/mcp-spec.md.
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
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
func EncodeFrame(v any) ([]byte, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(body))
	return append([]byte(header), body...), nil
}

// ParseFrames pulls all complete frames out of buf and returns the remainder.
// A partial trailing frame is returned in rest unchanged.
func ParseFrames(buf []byte) (msgs []Message, rest []byte, err error) {
	rest = buf
	for {
		headerEnd := bytes.Index(rest, []byte("\r\n\r\n"))
		if headerEnd < 0 {
			return msgs, rest, nil
		}
		length, lerr := contentLength(rest[:headerEnd])
		if lerr != nil {
			return msgs, rest, lerr
		}
		bodyStart := headerEnd + 4
		bodyEnd := bodyStart + length
		if len(rest) < bodyEnd {
			return msgs, rest, nil
		}
		var msg Message
		if jerr := json.Unmarshal(rest[bodyStart:bodyEnd], &msg); jerr != nil {
			return msgs, rest, jerr
		}
		msgs = append(msgs, msg)
		rest = rest[bodyEnd:]
	}
}

func contentLength(header []byte) (int, error) {
	for _, line := range bytes.Split(header, []byte("\r\n")) {
		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		name := string(bytes.TrimSpace(line[:colon]))
		value := string(bytes.TrimSpace(line[colon+1:]))
		if !equalFold(name, "Content-Length") {
			continue
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("invalid Content-Length: %q", value)
		}
		return n, nil
	}
	return 0, fmt.Errorf("missing Content-Length header")
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
