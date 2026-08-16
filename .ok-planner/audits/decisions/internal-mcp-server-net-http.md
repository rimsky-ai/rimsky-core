---
audit: internal-mcp-server-net-http
artifact: decision:internal-mcp-server-net-http
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:43:41Z
---

# The per-dispatch MCP callback endpoint runs on the standard-library HTTP stack

Supported. The internal MCP server is built entirely from the Go standard library's HTTP package and its own hand-written JSON-RPC handling — a request multiplexer, one route, a session table, and a bearer check — with no MCP library in the services module's dependency set at all. The JSON-RPC surface it speaks is the small subset the CLI needs: initialize, tool listing, and tool calls covering six callback tools. Shutdown is graceful in two respects the decision names: the server closes through the standard graceful-shutdown call with a bounded grace period, falling back to a hard close only if that fails, and every terminal tool handler defers its teardown onto a separate goroutine so the tool's own JSON-RPC response is written before anything tears the endpoint down. Tests drive the terminal reports through a real client over the live endpoint and receive their responses before the dispatch resolves, which is that ordering. The same server constructor is called from exactly two places — the callback endpoint and the module-transport loopback endpoint — so the shared core the rationale describes is real, and the loopback path adds only its own bearer token and tool provider.
