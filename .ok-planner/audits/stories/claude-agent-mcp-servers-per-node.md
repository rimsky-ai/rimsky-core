---
audit: claude-agent-mcp-servers-per-node
artifact: story:claude-agent-mcp-servers-per-node
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:33:41Z
---

# Per-node MCP server declaration, operator boundary, intersection

Supported. `agentrun.go::resolveHostServers` rejects a node's `cli.mcp_servers` entry whose name is outside the `RIMSKY_CLAUDE_AGENT_MCP_ALLOWLIST` operator allowlist, naming the server, instance, and node (unit-tested by `TestRunAgentMcpAllowlistViolation`), and separately gates the `stdio` transport (arbitrary node-supplied command) behind an open allowlist (`TestRunAgentStdioBlockedWhenAllowlistClosed` / `TestRunAgentStdioAllowedWhenAllowlistOpen`). The per-node/operator split is proven end-to-end by a three-node scenario test in which each node declares a different MCP server (http, http, and module transport respectively) and each node's dispatch reaches only its own declared server — the other two are asserted absent from its observed `mcp_servers` — with the module-loopback node's tool call round-tripping through a live loopback server to confirm the transport actually resolved.
