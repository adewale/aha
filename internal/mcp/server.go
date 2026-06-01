package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Serve runs an MCP session on stdio against the given backend until
// the caller closes the transport (typically by closing stdin). Wire
// format and lifecycle are owned by the SDK Server; this is a thin
// entrypoint so callers don't have to reach into the SDK directly.
func Serve(b Backend) error {
	return NewServer(b).Run(context.Background(), &mcp.StdioTransport{})
}
