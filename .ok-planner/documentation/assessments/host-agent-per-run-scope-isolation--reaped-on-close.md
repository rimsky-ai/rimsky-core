---
assessment: host-agent-per-run-scope-isolation--reaped-on-close
subject: story:host-agent-per-run-scope-isolation
way: reaped-on-close
release: d977250c
outcome: held
warrant: experiment:host-agent-per-run-scope-isolation
---
# Having each child reaped when its run-scope closes

After the fan-out settled fresh, `catalog:cli-verbs/rimsky agent status` showed the agent's child list empty and no process was left running the bound binary, while the agent itself stayed connected. The children are therefore reaped on their run-scopes closing rather than on the agent stopping, so a long-lived agent driving many fan-outs does not accumulate processes. The reaping was read at the same surface that had listed all three children while they were in flight.

## Unverified remainder

None: the passing run demonstrates the way as promised.
