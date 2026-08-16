---
audit: host-agent-per-run-scope-isolation
artifact: story:host-agent-per-run-scope-isolation
text: compliant
implementation: supported
commit: PENDING
audited: 2026-08-16T05:15:00Z
---

# Three sibling run-scopes in one frame get three isolated children, each reaped when it closes

Supported. A fan-out node with three declared partitions and parallelism three
dispatched the same late-bound service, its binary holding each execution open
so all three siblings were in flight at once. At that moment the agent listed
three spawned children, each naming a different run-scope and carrying its own
spawn id, all from the one declared binding path, and three separate
operating-system processes were running the binary. The three executions
reported three different run-scopes and three different pids in a one-to-one
pairing, and every child reported its own in-memory counter at one, so no
process served a second run-scope's call and no run-scope saw another's
in-process state. After the fan-out settled fresh the agent's child list emptied
and no process was left running the binary, while the agent itself stayed
connected, having spawned three children in total — one per run-scope, not one
per dispatch and not one shared.
