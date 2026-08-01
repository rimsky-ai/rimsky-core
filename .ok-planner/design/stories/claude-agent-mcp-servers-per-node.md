---
story: claude-agent-mcp-servers-per-node
status: as-is
---

# Template authors declare per-node MCP servers; operators bound them

## Story

As a template author using the bundled claude-agent executor, I declare on each node the list of MCP servers that node's dispatch may reach (each server's transport type — http, stdio, or module — and its transport-appropriate parameters) inline in the node config, while the operator running the claude-agent service separately declares an allowlist restricting which MCP server references any template may use; the service enforces the intersection. So that template authors own per-node MCP surfaces and operators own the boundary of what's permitted, without either reaching across into the other's territory.

Per-node inline MCP server declarations in `cli.mcp_servers` cover the three supported transports (http, stdio, module). The operator allowlist lives on the claude-agent process's env; the handler enforces the intersection at dispatch. No operator-side rimsky.yml block keyed by node name is needed to make each node's declaration effective.

Template authors own per-node MCP surfaces; operators own the boundary of what's permitted; neither reaches into the other's territory.
