---
assessment: claude-agent--per-node-declarations
subject: story:claude-agent
way: per-node-declarations
release: d977250c
outcome: held
warrant: experiment:claude-agent
---
# A node's own tool and environment declarations meet the operator's bounds

Both operator allowlists were set to one entry each for the run. The worker node's agent received exactly its node's own inline tool server plus the executor's callback server, and exactly its node's own declared environment variable at the operator-set value. The template author's per-node declaration and the operator's allowlist therefore met at the intersection: the node got what it asked for because the operator had permitted it, and nothing more.

## Unverified remainder

This way shows the intersection where the two sides agree; what happens when a node declares something outside the operator's allowlist is the subject of the per-node tool and environment stories.
