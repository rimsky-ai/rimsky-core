---
assessment: claude-agent-expose-env-per-node--allowlist-set
subject: story:claude-agent-expose-env-per-node
way: allowlist-set
release: d977250c
outcome: held
warrant: experiment:claude-agent-expose-env-per-node
---
# With an operator allowlist in force, each node reads only its own permitted variables

The audit drove a deployment of `catalog:images/rimsky-all-in-one` running the bundled agent executor with a stand-in agent that reports only digests of the variables it can read, on one template whose three agent nodes each declare a different single variable. Ten checks ran and none failed. With the operator allowlist naming two of the three variables, each permitted node read exactly its own declared variable at the operator-set value and neither read the other's. The node declaring the variable outside the allowlist failed its dispatch with `catalog:error-classes/agent/attribute_invalid`, and the error named the variable, the instance, the node and the allowlist itself. The enforcement is the intersection, checked from both sides: a node gets a variable only if it declared it and the operator permitted it.

## Unverified remainder

None: the passing run demonstrates the way as promised.
