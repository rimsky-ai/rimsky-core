---
assessment: host-agent-per-binding-overrides--spawn-timeout
subject: story:host-agent-per-binding-overrides
way: spawn-timeout
release: d977250c
outcome: held
warrant: experiment:host-agent-per-binding-overrides
---
# Giving each late-bind binding its own spawn timeout

The spawn timeout was exercised in both directions on a binary that holds its startup for twenty seconds. With a two-second timeout declared on the binding, the node settled failed carrying the agent's `catalog:error-classes/spawn_failed`. With a sixty-second timeout declared and nothing else changed, the same binary spawned, served the dispatch, and the node settled fresh. The timeout is therefore the binding's own number rather than a deployment-wide one, so a slow-starting binary and a fast one can share an agent.

## Unverified remainder

None: the passing run demonstrates the way as promised.
