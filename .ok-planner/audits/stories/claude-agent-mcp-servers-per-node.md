---
audit: claude-agent-mcp-servers-per-node
artifact: story:claude-agent-mcp-servers-per-node
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# Per-node MCP declarations and the operator allowlist that bounds them

Supported. One template declaring three agent nodes, each naming a different
single inline MCP server, was run twice against an all-in-one deployment
carrying the bundled claude-agent executor: once with an operator allowlist
naming two of the three servers, once with the allowlist unset. Each node's
agent received exactly the server that node declared, plus the executor's own
callback server, and never the server another node declared. The node naming the
server outside the allowlist failed its dispatch, and the refusal named the
server, the instance, the node and the allowlist variable. With the allowlist
unset the same node ran and reached that same server, which is what makes the
refusal the operator's act rather than the template's. Neither side reached into
the other's territory: the operator never named a node, and no node changed what
the allowlist admits.
