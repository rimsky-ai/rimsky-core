---
assessment: anonymous-agents-isolated--per-agent-routing
subject: story:anonymous-agents-isolated
way: per-agent-routing
release: d977250c
outcome: held
warrant: experiment:anonymous-agents-isolated
---
# Each developer's instance dispatches only to that developer's own agent

Two instances were created against the shared anonymous deployment, each naming one of the two running agents as its target, and the deployment stamped each instance with that agent's routing identity. Both dispatches settled fresh, and each carried back the writeback of the binary its own agent had spawned, so the two developers got two different results from the same instance shape. Each agent's own record showed exactly one spawn, one child announcing that agent's label, and exactly one execution — neither agent saw the other's dispatch. The isolation is in the routing, not in the timing: both agents were connected throughout.

## Unverified remainder

None: the passing run demonstrates the way as promised.
