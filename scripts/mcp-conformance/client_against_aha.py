"""Conformance test: official MCP SDK client → aha mcp server.

Spawns `aha mcp` and drives it through the official Anthropic MCP Python
SDK (`mcp.ClientSession` + `mcp.client.stdio.stdio_client`). The SDK speaks
the real MCP stdio wire format (newline-delimited JSON), so a successful
run proves aha's server is wire-compliant. Tested handshake, tool listing,
and one round-trip tool call against a corpus populated in setup.

Skips with exit 77 (autotools convention) if AHA_CONFIG isn't set; the
verify script supplies that path after building a fixture corpus.
"""

import asyncio
import json
import os
import sys

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client

# Tools aha exposes today. Update this list when readOnlyTools in
# internal/mcp/tools.go changes — the test asserts exact equality.
EXPECTED_TOOLS = [
    "search",
    "read",
    "status",
    "verify",
    "conflicts",
    "corpus_size",
    "doctor",
]


async def main() -> int:
    aha_bin = os.environ.get("AHA_BIN", "/tmp/aha")
    config = os.environ.get("AHA_CONFIG")
    if not config:
        print("AHA_CONFIG unset; skipping", file=sys.stderr)
        return 77

    params = StdioServerParameters(command=aha_bin, args=["mcp", "--config", config])

    async with stdio_client(params) as (read, write):
        async with ClientSession(read, write) as session:
            init = await session.initialize()
            assert init.serverInfo.name == "aha", f"serverInfo.name={init.serverInfo.name!r}"
            print(f"initialize OK: protocol={init.protocolVersion} server={init.serverInfo.name} {init.serverInfo.version}")

            listed = await session.list_tools()
            got = sorted(t.name for t in listed.tools)
            want = sorted(EXPECTED_TOOLS)
            assert got == want, f"tools/list mismatch: got={got} want={want}"
            print(f"tools/list OK: {got}")

            # Read-only hint must be advertised. (2025-06-18 spec.)
            for t in listed.tools:
                ann = (t.annotations.model_dump() if t.annotations else {})
                assert ann.get("readOnlyHint") is True, f"{t.name}: readOnlyHint not True ({ann})"
            print(f"tool annotations OK: readOnlyHint=true on all {len(listed.tools)} tools")

            # Round-trip a no-arg tool that always succeeds against an empty corpus.
            res = await session.call_tool("status", {})
            assert res.content, f"call_tool(status) returned no content blocks"
            text = res.content[0].text
            status = json.loads(text)
            for key in ("corpus_dir", "sessions", "entries", "fts_messages"):
                assert key in status, f"status missing {key!r}: {list(status.keys())[:10]}"
            print(f"tools/call status OK: sessions={status.get('sessions')} entries={status.get('entries')}")

            # If the server exposes structuredContent (spec 2025-06-18), it
            # should equal the parsed text payload.
            if getattr(res, "structuredContent", None) is not None:
                assert res.structuredContent == status, "structuredContent != parsed text content"
                print("tools/call structuredContent OK: matches text content")

            # Tool that takes args. The fixture seeded a user message "hello"
            # so the search guarantees at least one hit; otherwise we couldn't
            # tell empty-result handling from broken framing.
            res = await session.call_tool("search", {"query": "hello", "limit": 5})
            assert res.content, "call_tool(search) returned no content"
            hits = json.loads(res.content[0].text)
            assert isinstance(hits, list), f"search result is not a list: {type(hits).__name__}"
            assert len(hits) >= 1, f"search('hello') returned no hits: {hits}"
            assert all("ref_text" in h for h in hits), f"hit missing ref_text: {hits[0]}"
            print(f"tools/call search OK: returned {len(hits)} hits")

            # Empty-result query: serialized payload should be an empty
            # list, not JSON null. Guards against Go's nil-slice → null
            # marshalling regression.
            res = await session.call_tool("search", {"query": "definitelynotinthecorpus", "limit": 5})
            empty = json.loads(res.content[0].text)
            assert empty == [], f"empty search result not []: {empty!r}"
            print(f"tools/call search empty-result OK: payload is []")

            # Strict-validation: unknown arg must be rejected.
            try:
                await session.call_tool("search", {"query": "x", "bogus": 1})
            except Exception as e:
                msg = str(e)
                assert "unexpected argument" in msg or "bogus" in msg, f"unexpected error: {msg!r}"
                print(f"tools/call unknown-arg rejection OK")
            else:
                print("FAIL: unknown-arg search call was accepted", file=sys.stderr)
                return 1

            # Unknown tool must error.
            try:
                await session.call_tool("refresh", {})
            except Exception as e:
                print(f"tools/call unknown-tool rejection OK")
            else:
                print("FAIL: unknown tool call was accepted", file=sys.stderr)
                return 1

    print("aha MCP server is conformant under the official Python SDK client.")
    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
