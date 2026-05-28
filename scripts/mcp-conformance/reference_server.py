"""Reference MCP server (official SDK) used to validate aha's TS client.

Our `clients/typescript/transports/stdio.ts` should be able to drive any
spec-compliant MCP stdio server, not just `aha mcp`. This script provides a
tiny FastMCP server with three tools so the TS test harness can prove its
NDJSON framing and handshake interoperate with the official SDK end of
things.

Run directly:  python3 scripts/mcp-conformance/reference_server.py
The harness spawns it as a child and reads/writes over stdio.
"""

from mcp.server.fastmcp import FastMCP


mcp = FastMCP("reference-mcp")


@mcp.tool()
def echo(text: str) -> str:
    """Echo the input text. Used by the TS client harness to round-trip a payload."""
    return text


@mcp.tool()
def add(a: int, b: int) -> int:
    """Return a + b. Exercises typed numeric arguments."""
    return a + b


@mcp.tool()
def fail() -> str:
    """Always raise. Exercises error propagation back through the transport."""
    raise RuntimeError("intentional")


if __name__ == "__main__":
    mcp.run(transport="stdio")
