---
audit: host-agent-per-run-scope-isolation
artifact: story:host-agent-per-run-scope-isolation
determination: supported
compliance: compliant
commit: PENDING
audited: 2026-08-10T07:40:00Z
---

# One child per sibling run-scope, sharing nothing, reaped when the scope closes

Supported, measured on a fan-out node whose three sibling run-scopes dispatched
the same late-bound service concurrently, each execution held open so all three
were in flight at once. The agent listed three spawned children at that moment,
each naming a different run-scope with its own spawn id, and three separate
operating-system processes were running the bound binary. The three executions
reported three different run-scopes, three different pids, and a one-to-one
pairing between them; every child reported the counter it keeps in its own
memory at one, so no process served a second run-scope's call. After the fan-out
settled fresh the agent's child list emptied and no process was left running the
binary, while the agent itself stayed connected — three children spawned in
total, one per run-scope.
