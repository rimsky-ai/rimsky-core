---
story: claude-agent-mcp-servers-per-node
status: as-is
---

# Template authors declare per-node MCP servers; operators bound them

## Story

As a template author using the bundled claude-agent executor, I declare per node which MCP servers that node's dispatch may reach, while the operator running the claude-agent service separately bounds which MCP servers any template may use, and the service enforces the intersection — so that template authors own per-node tool surfaces and operators own the boundary of what's permitted, without either reaching into the other's territory.
