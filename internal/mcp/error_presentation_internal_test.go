package mcp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestErrorResultHidesRawCauseAndProvidesOneAction(t *testing.T) {
	result := errorResult(errors.New("secret-canary /private/corpus.db select * from messages"))
	if !result.IsError || len(result.Content) != 1 {
		t.Fatalf("result=%+v", result)
	}
	text, ok := result.Content[0].(*sdkmcp.TextContent)
	if !ok {
		t.Fatalf("content type=%T", result.Content[0])
	}
	body := fmt.Sprint(text.Text)
	if strings.Contains(body, "secret-canary") || strings.Contains(body, "/private/") || strings.Contains(body, "select *") {
		t.Fatalf("MCP error leaked raw cause: %q", body)
	}
	if strings.Count(body, "next:") != 1 {
		t.Fatalf("MCP error=%q want exactly one action", body)
	}
}
