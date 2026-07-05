---
story: claude-agent-mcp-servers-per-node
status: as-is
---

# Template authors declare per-node MCP servers; operators bound them

## Role

As a template author using the bundled claude-agent executor, I declare on each node the list of MCP servers that node's dispatch may reach (each server's transport type — http, stdio, or module — and its transport-appropriate parameters) inline in the node config, while the operator running the claude-agent service separately declares an allowlist restricting which MCP server references any template may use; the service enforces the intersection. So that template authors own per-node MCP surfaces and operators own the boundary of what's permitted, without either reaching across into the other's territory.

## Capability

Per-node inline MCP server declarations in `cli.mcp_servers` cover the three supported transports (http, stdio, module). The operator allowlist lives on the claude-agent process's env; the handler enforces the intersection at dispatch. No operator-side rimsky.yml block keyed by node name is needed to make each node's declaration effective.

## Business value

Template authors own per-node MCP surfaces; operators own the boundary of what's permitted; neither reaches into the other's territory.

## Acceptance

I author a template with two claude-agent nodes, each declaring a different MCP server list in its node config, drawing from any of the three supported transports. The operator's allowlist covers both nodes' declared servers. Each node dispatches; each real handler's MCP surface at spawn matches its own node's declaration observably — the CLI child's MCP config reflects that node's servers, and the child's MCP calls reach exactly the servers that node declared and no others. Separately, when the operator's allowlist excludes a server that a node declares, the service rejects that dispatch with an operator-facing error naming the disallowed server, the template, and the node.

## Falsifier

Two claude-agent nodes with different declared MCP server lists observably reach the same MCP surface at spawn; OR a node declaring an MCP server outside the operator's allowlist dispatches anyway and reaches the disallowed server; OR the inline schema retains only a subset of the three transports (any of http / stdio / module becomes unreachable via per-node declaration); OR the per-node MCP list requires operator-side keying (rimsky.yml, service container env keyed by node); OR the rejection error is generic (doesn't name the disallowed server, template, or node); OR rimsky's own dispatch payload or protocol shape gains an MCP-related field (the redesign must live inside the claude-agent service, not in rimsky's dispatch path).

## Proof

Executable proof — a scenario test registers a template with claude-agent nodes declaring different MCP server sets in their node configs, with declarations together covering all three supported transports (http, stdio, module); the operator config allowlists cover the declared servers. The test dispatches the nodes and asserts observable per-node MCP behavior — each spawned CLI child reaches exactly the servers its own node declared and no others. A second scenario adds a node declaring an off-allowlist MCP server; the test asserts the service rejects that dispatch with an error naming the disallowed server, template, and node. An additional assertion greps rimsky's protocol surface for MCP-related fields and confirms none were added by this spec.
