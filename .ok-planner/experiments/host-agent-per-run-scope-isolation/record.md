---
experiment: host-agent-per-run-scope-isolation
commit: d977250c
---

# Three sibling run-scopes, three children, no shared process

## What it ran against

A `rimsky-all-in-one` stack with the bundled filesystem claim producer
configured over a bind-mounted workspace, a `rimsky-host-agent-proxy`, both from
the tree's own image tag, and a `rimsky` CLI host-agent on the host. The
template's node fans out over three declared partitions with parallelism three,
and its executor is a late-bound service bound to the local binary built for
host-agent-late-bind-all-protocols. The binding hands the binary a delay it
holds each execution open for, so all three sibling run-scopes are in flight at
once. The binary reports its pid, the run-scope it was called for, and a counter
it keeps in its own memory across calls. Re-run unchanged at this tree.

## What was observed

While the three siblings were in flight, `rimsky agent status` listed three
spawned children, each naming a different run-scope, each with its own spawn id,
all from the one declared binding path. Three separate operating-system
processes were running the bound binary at that moment.

The three executions reported three different run-scopes, three different pids,
and a one-to-one pairing between them. Every child reported its in-memory
counter at one, so no process served a second run-scope's call and no run-scope
saw another's in-process state.

After the fan-out settled fresh, the agent's child list emptied and no process
was left running the bound binary, while the agent itself stayed connected. The
agent had spawned three children in total — one per run-scope, not one per
dispatch and not one shared.

RESULT: PASS
