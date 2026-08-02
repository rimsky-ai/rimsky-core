---
audit: mcp-transport
artifact: story:mcp-transport
determination: unsupported
commit: b767a27d
audited: 2026-08-02T09:58:10Z
issue: 2026-08-02-095826-mcp-catalog-missing-four-http-routes
---

# Operator/agent drives rimsky entirely via MCP

Unsupported. This story's promise is the operational form of a sibling decision's parity claim, and the same check settles both: enumerating the full population of control-API actions against the tool catalog that is meant to project them finds four routes — two instance-frame reads, one observability-read, and service enrollment — with no MCP tool and no covering resource. An MCP-only client cannot reach these four surfaces, so it does not yet get every read and mutation the platform offers through MCP alone.
