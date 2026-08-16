---
assessment: claude-agent-expose-env-per-node--secrets-absent-from-state
subject: story:claude-agent-expose-env-per-node
way: secrets-absent-from-state
release: d977250c
outcome: held
warrant: experiment:claude-agent-expose-env-per-node
---
# Exposed secret values do not land in the deployment's persisted state

The secrecy claim was taken as a count over a named population rather than as a spot check: none of the three plaintext values appears in any of the five persisted surfaces the run read back — the instance's event log, its node-run records, the instance record, the audit log, or the per-node attributes. An operator exposing a credential to one agent node therefore does not thereby write that credential into the deployment's own history, where it would outlive the dispatch and be readable by anyone who can read the instance.

## Unverified remainder

The five surfaces read back are rimsky's own persisted state; what an agent does with a value once it has it belongs to that agent.
