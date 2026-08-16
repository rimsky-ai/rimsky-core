---
experiment: assumption-runtime-diagnostics-are-actionable
commit: d977250c
---

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
