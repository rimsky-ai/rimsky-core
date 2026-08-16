---
experiment: iterative-workflows-converge
commit: d977250c
---

# A self-cycle and a two-node cycle terminating on a declared predicate

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image at
`RIMSKY_IMAGE_TAG`, on its zero-config SQLite defaults. `run.py` registers two
templates through the control API, wakes each instance with one operator
message, waits for every frame to settle and every node run to end, then reads
the event stream and the frame list back off the observability routes. It boots
and removes its own container.

Both templates iterate with the builtin `loop_counter` node kind, whose `max`
attribute is set to 50 — far above the three rounds either cycle runs. What
stops each cycle is the subscription predicate the template declares:
`payload.attributes_delta.count < 3` on the back edge, and
`payload.attributes_delta.count >= 3` on the edge leaving the cycle.

## What was observed

Two legs, nine checks, none failing.

In the self-cycle template the iterating node subscribes to its own
`terminal/success` under the back-edge predicate, and carries
`cascade_mode: sequenced`. It emitted counts 1, 2, 3 and stopped. The
downstream node, subscribed under the leaving predicate, ran exactly once. The
whole iteration is one frame, in state `completed`.

In the two-node template a seed node starts `ping`, `pong` subscribes to
`ping` under the back-edge predicate, and `ping` subscribes back to `pong`.
`ping` emitted counts 1, 2, 3; `pong` ran twice, once per round below the
predicate; the downstream node ran once on the converged output. That cycle is
also one frame, in state `completed`, and the instance came to rest with no
live node runs.
