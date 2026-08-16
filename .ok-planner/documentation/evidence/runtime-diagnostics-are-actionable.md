---
trap: runtime-diagnostics-are-actionable
release: d977250c
---
# Evidence set — the four `/v1/admin/diagnostics/*` reads have matching remediation actions, so anything they report as stuck can be unstuck through the API.

Source of the prior: sibling-symmetry — four read-only diagnostics routes (`held-frames`, `parked-nodes`, `producer-outbox`, `wait-sets`) with only `node:reset` and `instance:kill` as write counterparts

## What the audit ran and observed (assumption record)

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

## Experiment record (experiment:assumption-runtime-diagnostics-are-actionable)

# Can what the diagnostics reads report as stuck be unstuck through the API?

## What it ran against

A `rimsky-all-in-one` stack from the tree's own image tag on a free port, the
bundled `rimsky-claim-producer-filesystem` service, and `peer/` — a third-party
executor built for the run — on their own docker network. The template wedges an
instance deliberately: a claim-holding node parks for 24 hours, a second node
co-holds its claim, and a receiver declares a force-refreshed dependency on the
parked node. Once the wedge forms, every plausible remediation is driven through
the control API and the CLI, and the four diagnostics reads are re-read after
each. The store is never opened.

## What was observed

The four reads report the wedge: one parked node with its `parked_at` and
`resume_at`, one held frame naming that node, three pending wake edges on the
frame's wait-set, and a producer outbox at depth 0.

Nothing un-parks the node the roster names. `POST /v1/nodes/{id}/reset` refuses
409 — `reset only valid when node has a failed terminal run in some scope` — and
a parked node is not a failed one. Five candidate resume paths are not routes at
all (`/v1/nodes/{id}/resume`, `/v1/nodes/{id}/unpark`,
`/v1/parked/{id}/resume`, `/v1/instances/{id}/parked/{node}/resume`,
`/v1/instances/{id}/nodes/{node}/resume`), each chi's `404 page not found`, and
`rimsky parked` offers only `list`.

The instance-level levers do not clear it either. `POST /v1/instances/{id}/resume`
answers 409 on an unpaused instance and the node stays parked. The debug override
is refused while the instance is neither paused nor at a breakpoint (409,
`instance not in debuggable state`, `states: [paused, breakpoint]`); pausing the
instance makes it apply (200, `runs_mutated: 1`) and the node is still parked
afterwards.

Nor is there a route for the other three findings: held-frame release and cancel,
producer-outbox retry and drain, and wait-set clear are all 404, and DELETE on
the wait-set route is 405.

The one lever that clears the board is demolition. `POST /v1/instances/{id}/terminate`
is accepted 200, and once the instance carries a termination time the park roster
and the held-frame roster no longer name it.

EXPERIMENT PASS (24 checks)

Runnables: `src:.ok-planner/experiments/assumption-runtime-diagnostics-are-actionable/` at the stamped commit.
