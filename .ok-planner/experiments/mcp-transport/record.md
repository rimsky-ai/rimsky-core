---
experiment: mcp-transport
commit: PENDING
---

# Every permissioned action, reached over MCP, under the same auth answers

## What it ran against

A `rimsky-all-in-one` stack from the tree's own image tag, driven by a plain
JSON-RPC client speaking the control API's MCP endpoint. The run mints the
deployment's first admin key with `rimsky auth init`, then two narrower keys
through the key-creation route: one holding every read, one holding a single
grant no route in the sweep needs. `routes.tsv` carries the 82 ruled public
control-API routes with a concrete request for each.

The deployment's own audit log is the instrument for both mappings. Every
gated request is recorded with the action it resolved to, the request path, the
response status and the protocol it arrived over, and an MCP tool call is
re-dispatched through the same gate carrying the protocol `mcp`, so which
action a route resolves to and which action a tool reaches are both read back
from the product.

## What was observed

Driving all 82 ruled routes over plain HTTP resolved them to 44 distinct gated
actions. Each of the 44 was refused (401) without a token and refused (403)
with the unrelated-grant key, so all 44 are permissioned. The three routes that
answer without a permission — the liveness probe, the identity echo, and the CA
root — are not gated and name no action.

The MCP client opened a session, was offered 57 tools, and every one of them
declared an input schema and a description. Calling all 57 reached an action
over the MCP protocol in every case — 57 of 57 — covering 43 of the 44
permissioned actions. The forty-fourth is the MCP dispatch surface itself,
which the client performs on every call it makes and which the deployment
records against the client's key with a 200. No permissioned action was left
unreachable.

The client then did real work without leaving the endpoint: it validated,
registered and deployed a template, created an instance, woke it, listed and
read its node, read its messages and the event log, paused and resumed it,
killed it, deleted it, then undeployed and deleted the template. The deleted
instance answered 404 when read afterwards over plain HTTP, so the mutations
landed in the deployment rather than in the transport.

The auth answers matched the HTTP surface at every point tested. A caller with
no token was refused 401 on MCP exactly as on an ordinary route. The read-only
key was offered 30 of the 57 tools, none of them a write, and calling a write
tool was refused — the same key getting 403 from the corresponding HTTP route.
The deployment attributed the read-only key's MCP work to that key by name and
id. Revoking the key ended its MCP session-opening with 401, identically to the
HTTP route. An unknown tool name was refused rather than dispatched.

RESULT: PASS
