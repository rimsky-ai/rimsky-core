---
assessment: claude-agent-mcp-servers-per-node--allowlist-set
subject: story:claude-agent-mcp-servers-per-node
way: allowlist-set
release: d977250c
outcome: held
warrant: experiment:claude-agent-mcp-servers-per-node
---
# With an operator allowlist in force, each node's agent sees only its own permitted servers

The audit drove a deployment of `catalog:images/rimsky-all-in-one` running the bundled agent executor against a stand-in agent that reports the server configuration handed to it, on one template whose three agent nodes each declare a different single inline server. Seven checks ran and none failed. With the operator allowlist naming two of the three servers, each permitted node's agent saw exactly its own declared server plus the executor's own callback server, and never the other node's. The node declaring the server outside the allowlist failed its dispatch with `catalog:error-classes/agent/attribute_invalid`, and the error named the server, the instance, the node and the allowlist itself. A template author therefore cannot widen the operator's boundary by declaring a server, and one node's tool surface is not another node's.

## Unverified remainder

None: the passing run demonstrates the way as promised.
