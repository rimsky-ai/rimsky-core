---
assessment: host-agent-per-run-scope-isolation--isolated-children
subject: story:host-agent-per-run-scope-isolation
way: isolated-children
release: d977250c
outcome: held
warrant: experiment:host-agent-per-run-scope-isolation
---
# Trusting sibling run-scopes in one frame not to share an executor process

A fan-out node with three declared partitions and `catalog:template-keys/nodes[].fan_out.parallelism` of three dispatched the same late-bound service, its binary holding each execution open so all three siblings were in flight at once. At that moment `catalog:cli-verbs/rimsky agent status` listed three spawned children, each naming a different run-scope and carrying its own spawn id, all from the one declared binding path, and three separate operating-system processes were running the binary. The three executions reported three different run-scopes and three different process ids in a one-to-one pairing, and every child reported its own in-memory counter at one, so no process served a second run-scope's call and no run-scope saw another's in-process state. The agent spawned three children in total — one per run-scope, not one per dispatch and not one shared.

## Unverified remainder

None: the passing run demonstrates the way as promised.
