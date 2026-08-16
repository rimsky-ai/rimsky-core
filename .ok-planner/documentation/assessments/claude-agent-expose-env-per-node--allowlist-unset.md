---
assessment: claude-agent-expose-env-per-node--allowlist-unset
subject: story:claude-agent-expose-env-per-node
way: allowlist-unset
release: d977250c
outcome: held
warrant: experiment:claude-agent-expose-env-per-node
---
# With no operator allowlist, the node's own declaration is still the boundary

The same template was run again with the operator allowlist unset. The third node then read the variable it declares, and the other two still read only their own declarations — an unset allowlist widens the operator's boundary, and does not dissolve the author's. The per-node declaration is the author's boundary and the allowlist is the operator's, and neither reaches into the other's territory.

## Unverified remainder

None: the passing run demonstrates the way as promised.
