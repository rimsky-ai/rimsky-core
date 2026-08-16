---
assessment: host-agent-control-plane--stop
subject: story:host-agent-control-plane
way: stop
release: d977250c
outcome: held
warrant: experiment:host-agent-control-plane
---
# Stopping the agent cleanly, with its children reaped

`catalog:cli-verbs/rimsky agent stop` returned success and reported the agent stopped while a spawned child was live. Afterwards the child process was gone, and no process anywhere on the machine still held the bound binary open, so stopping the agent leaves no orphan behind for the operator to hunt down. `catalog:cli-verbs/rimsky agent status` reported not running again, and a second stop returned success rather than an error, so the verb is safe to issue when the operator is unsure of the current state.

## Unverified remainder

None: the passing run demonstrates the way as promised.
