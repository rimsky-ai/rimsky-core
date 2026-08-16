---
audit: claude-agent-mcp-servers-per-node
artifact: story:claude-agent-mcp-servers-per-node
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T04:58:36Z
---

# Per-node MCP declarations meet an operator allowlist, and neither side reaches into the other

Supported. Driven through the public surface against a released-image stack
running the bundled agent executor with a stand-in agent binary that reports the
server configuration handed to it, on one template whose three agent nodes each
declare a different single inline server, run twice — once with an operator
allowlist naming two of the three, once with no allowlist. Seven checks, none
failing. With the allowlist set, each permitted node's agent saw exactly its own
declared server plus the executor's own callback server and never the other
node's, while the node declaring the server outside the allowlist failed its
dispatch with an attribute-invalid error naming the server, the instance, the node
and the allowlist itself. With the allowlist unset the third node ran and its
agent saw its own server, and the other two still saw only their own
declarations — so the template author's per-node surface and the operator's
boundary each hold without either widening the other.
