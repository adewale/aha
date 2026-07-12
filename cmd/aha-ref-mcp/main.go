// aha-ref-mcp is a minimal MCP reference server built on the official Go
// SDK (`github.com/modelcontextprotocol/go-sdk/mcp`). It's used by the
// conformance harness to validate aha's stdio Transport: our TS client
// drives this server end-to-end and proves the framing/handshake/result
// shapes interoperate with a third independent SDK implementation
// alongside the Python FastMCP and TypeScript McpServer references.
//
// The exact tool surface is pinned in
// scripts/mcp-conformance/REFERENCE.md. The Python (reference_server.py)
// and TypeScript (reference_server.ts) reference servers in this repo
// must match this file's surface tool-for-tool — the cross-language
// conformance harness relies on the equivalence.
package main

import (
	"context"
	"errors"
	"strconv"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type echoArgs struct {
	Text string `json:"text" jsonschema:"the text to echo"`
}

type addArgs struct {
	A float64 `json:"a" jsonschema:"first addend"`
	B float64 `json:"b" jsonschema:"second addend"`
}

func main() {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "aha-ref-mcp-go",
		Version: "0.1.0",
	}, nil)

	mcp.AddTool(server,
		&mcp.Tool{Name: "aha_capabilities", Description: "Advertise the aha compatibility contract for transport conformance."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"schema":"aha.mcp.v2","http_schema":"aha.http.v2","required_features":["read-only-v1","strict-input-v1","structured-errors-v1"],"tools":["aha_capabilities","echo","add","fail"]}`}}}, nil, nil
		})

	mcp.AddTool(server,
		&mcp.Tool{Name: "echo", Description: "Echo the input text."},
		func(_ context.Context, _ *mcp.CallToolRequest, args echoArgs) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: args.Text}},
			}, nil, nil
		})

	mcp.AddTool(server,
		&mcp.Tool{Name: "add", Description: "Return a + b. Exercises typed numeric arguments."},
		func(_ context.Context, _ *mcp.CallToolRequest, args addArgs) (*mcp.CallToolResult, any, error) {
			sum := args.A + args.B
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: strconv.FormatFloat(sum, 'f', -1, 64)}},
			}, nil, nil
		})

	mcp.AddTool(server,
		&mcp.Tool{Name: "fail", Description: "Always errors. Exercises error propagation."},
		func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
			return nil, nil, errors.New("intentional")
		})

	ctx := context.Background()
	if err := server.Run(ctx, &mcp.StdioTransport{}); err != nil {
		// Avoid stdout (the protocol channel); the SDK should already have
		// drained stdin on exit but be defensive.
		_ = err
	}
}
