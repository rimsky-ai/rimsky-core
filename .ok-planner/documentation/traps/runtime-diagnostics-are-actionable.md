---
trap: runtime-diagnostics-are-actionable
release: d977250c
demonstration: experiment:assumption-runtime-diagnostics-are-actionable
---
## Assumption

As operator with a wedged instance, I would take it that the four `/v1/admin/diagnostics/*` reads have matching remediation actions, so anything they report as stuck can be unstuck through the API.

sibling-symmetry — four read-only diagnostics routes (`held-frames`, `parked-nodes`, `producer-outbox`, `wait-sets`) with only `node:reset` and `instance:kill` as write counterparts

## Actual behavior

Experiment `assumption-runtime-diagnostics-are-actionable`, run at this tree
against a stack, the bundled filesystem claim producer and a third-party executor
built for the run, with an instance wedged on purpose: a claim-holding node
parked for 24 hours, a co-holder, and a receiver waiting on a force-refreshed
dependency. All four reads report the wedge — one parked node, one held frame,
three pending wake edges, a producer outbox at depth 0 — and no read has a
matching remediation. `POST /v1/nodes/{id}/reset` refuses 409 (`reset only valid
when node has a failed terminal run in some scope`); five candidate un-park paths
are not routes (chi `404 page not found`) and `rimsky parked` offers only `list`.
Held-frame release and cancel, producer-outbox retry and drain, and wait-set
clear are 404, and DELETE on the wait-set route is 405. The instance-level levers
do not help: `POST /v1/instances/{id}/resume` answers 409 on an unpaused instance
and the node stays parked; the debug override is refused until the instance is
paused (409, `instance not in debuggable state`), then applies (200,
`runs_mutated: 1`) with the node still parked afterwards. The only action that
clears the findings is `POST /v1/instances/{id}/terminate` — after which the park
and held-frame rosters no longer name the instance, because the instance is gone.
The operator with a wedged instance can read exactly why it is stuck and can
demolish it, but cannot unstick it.
