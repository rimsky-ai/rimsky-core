---
audit: mcp-transport
artifact: story:mcp-transport
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
checked: 44
unaccounted: 0
---

# Every permissioned action reachable over MCP, with the same auth answers

Supported, measured by a JSON-RPC client driving the MCP endpoint of a stack
whose own audit log records each request's action, response status and the
protocol it arrived over. The population is the 44 gated actions the 82 ruled
public control-API routes resolved to when driven over plain HTTP; all 44 were
refused without a token and refused with a key holding an unrelated grant, so
all 44 are permissioned. The client was offered 57 tools, every one carrying an
input schema and a description, and calling all 57 reached an action over MCP in
every case, covering 43 of the 44; the forty-fourth is the MCP dispatch surface
itself, which the client performs on every call and which the deployment records
against the client's key. The client also did real work without leaving the
endpoint — validating, registering, deploying, instantiating, waking, reading,
pausing, resuming, killing and deleting, with the deletion confirmed over plain
HTTP. The auth answers match the rest of the surface: no token is refused 401 on
MCP exactly as on an ordinary route, a read-only key is offered 30 of the 57
tools with no write among them and is refused a write tool as the same key is
refused 403 by the corresponding route, the deployment attributes the key's MCP
work to that key, a revoked key can no longer open a session, and an unknown
tool name is refused rather than dispatched.
