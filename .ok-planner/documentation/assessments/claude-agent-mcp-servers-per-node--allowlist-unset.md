---
assessment: claude-agent-mcp-servers-per-node--allowlist-unset
subject: story:claude-agent-mcp-servers-per-node
way: allowlist-unset
release: d977250c
outcome: held
warrant: experiment:claude-agent-mcp-servers-per-node
---
# With no operator allowlist, per-node declarations still separate the nodes

The same template was run again with the operator allowlist unset. The third node then ran and its agent saw the server it declares, while the other two still saw only their own declarations. Removing the operator's boundary therefore does not merge the nodes' tool surfaces: what a node's agent may reach remains what that node declared, which is what makes the per-node declaration meaningful in a zero-configuration deployment as well as a bounded one.

## Unverified remainder

None: the passing run demonstrates the way as promised.
