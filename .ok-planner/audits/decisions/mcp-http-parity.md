---
audit: mcp-http-parity
artifact: decision:mcp-http-parity
determination: unsupported
commit: 3918d24e
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095826-mcp-catalog-missing-four-http-routes
---

# The MCP tool surface mirrors the HTTP control surface

Unsupported. The parity mechanism is architecturally sound where a tool exists — every registered tool call re-dispatches as a synthetic request through the identical router the HTTP clients use, and a test mechanically proves every mounted route has a registry entry. But the registry itself, checked against its full population of entries, leaves four real HTTP routes with neither a tool nor a covering resource: two instance-frame read routes, one observability-read route, and the service-enrollment route. No test enforces that every registered action carries at least one tool, so this population gap is unguarded.
