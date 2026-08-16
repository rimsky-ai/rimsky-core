---
audit: mcp-transport
artifact: story:mcp-transport
text: compliant
implementation: unsupported
commit: d977250c
audited: 2026-08-16T09:11:35Z
checked: 47
unaccounted: 3
---

# Every permissioned action reachable over MCP, three unpermissioned reads not

Unsupported on coverage. The story promises every read and mutation rimsky
offers, and the population it quantifies over is the public control-API surface,
not the permissioned part of it. Driving all 85 ruled public routes resolved
them to 47 declared actions: 44 gated by a permission, and three answering
without one — the liveness probe, the identity echo and the CA root. All 44
permissioned actions are reachable over MCP, and that half is solid: the client
was offered 57 tools, every one declaring an input schema and a description, and
calling all 57 reached an action over the MCP protocol, covering 43 — the
forty-fourth being the MCP dispatch itself, which the client performs on every
call. The three unpermissioned reads have no tool at all, so an agent asking the
two questions it asks first, whether the deployment is up and which key it is
holding, must leave MCP for HTTP and hold a second client to do it — which is
what the story's own purpose clause says it should not need. The gap is not
structural: all three are ordinary entries in the same action registry the
catalog is computed from, and each simply declares no tool name. The parity half
of the promise holds throughout, tested where a bypass would hide: a caller with
no token is refused identically on both surfaces, a read-only key is offered 30
tools with no write among them and is refused a write tool exactly as the
corresponding route refuses it, the deployment attributes the key's MCP work to
that key by name and id, a revoked key can no longer open a session just as it
can no longer use a route, and an unknown tool is refused rather than dispatched.
That the mutations are real was settled outside the transport: a full lifecycle
driven entirely over MCP left the deleted instance answering 404 to plain HTTP.

## Unaccounted

- the liveness probe read — answers over HTTP, no tool names it, and the
  observability system-health read is a different resource behind a different
  permission rather than a substitute
- the identity echo read — answers over HTTP for any valid token, no tool names it
- the CA-root read — answers over HTTP wherever peer authentication mounts it, no
  tool names it
