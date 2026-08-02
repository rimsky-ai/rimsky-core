---
decision: internal-mcp-server-net-http
---

# claude-agent's internal MCP server uses the standard-library HTTP stack

## Choice

The per-dispatch MCP callback endpoint the CLI child reports through is a standard-library HTTP server speaking the small JSON-RPC subset the CLI uses, shut down gracefully so an in-flight terminal report's response always reaches the child before the endpoint closes.

## Rationale

Standard library only; the JSON-RPC subset is small enough that a third-party MCP library would add dependency surface without ergonomic gain. The same server core also backs the loopback endpoints stood up for module-transport MCP declarations.

## Alternatives

Adopt a third-party Go MCP library — rejected: adds a dependency for a small verb surface.
