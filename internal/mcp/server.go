package mcp

import (
	"context"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Serve runs an MCP session on stdio against the given backend until the
// caller closes the transport (typically by closing stdin). The wire
// format and lifecycle are owned by the github.com/modelcontextprotocol/go-sdk
// Server; the function is a thin entrypoint kept here so callers don't
// have to reach into the SDK directly.
//
// The stderr parameter exists for backward compatibility with the
// pre-migration signature; the SDK already routes logs to stderr by
// default and we no longer need to interpose. It's accepted and ignored.
func Serve(b Backend, _ io.Reader, _ io.Writer, _ io.Writer) error {
	server := NewServer(b)
	return server.Run(context.Background(), &mcp.StdioTransport{})
}
