---
experiment: debug-channel
commit: PENDING
---

# The debug-override channel and the two states that open it

The story names two states that open the channel, so this directory holds two
runnable ways. Both also exercise the closed case.

## way-paused-gate.py

### What it ran against

The script creates a docker network, starts `rate-limited-endpoint.py` on it in a
`python:3.12-alpine` container, and boots a `rimsky-all-in-one` container on the
same network with `RIMSKY_EXECUTOR_HTTP_NODE_EGRESS_ALLOWLIST` set to the
network's subnet. The endpoint answers the first request with HTTP 429 and
`Retry-After: 3600`. The template declares a counter node, a passthrough knob
node, and an `http-node` worker that calls the endpoint, so the worker parks and
the frame stays open while the script works. No breakpoint is installed on this
instance.

### What was observed

Nine checks, none failing. With the instance running and a node parked, both
override actions answered HTTP 409 `instance not in debuggable state`, naming
`paused` and `breakpoint` as the states that would open the channel, and the
node's attribute values were unchanged afterward. After the instance was paused,
`set_attribute` on the parked worker answered 200 with `gate_state: paused` and
one run mutated, and the node read back with the operator's value on it.
`invalidate_node` on the knob node answered 200 on the same open channel, and no
work ran while the instance stayed paused. Resuming the instance ran the knob
node a second time.

## way-breakpoint-gate.py

### What it ran against

A `rimsky-all-in-one` container with no external endpoint. The template declares
a counter node, a passthrough knob node, and a passthrough worker. A pause-mode
pre-dispatch breakpoint on the worker holds its run in flight; the instance is
never paused.

### What was observed

Seven checks, none failing. At the unresumed hit, `set_attribute` on the worker
answered 200 with `gate_state: breakpoint`, and the value read back off the node.
`invalidate_node` on the knob node answered 200 on the same gate, and the knob
node ran again once the hit was released. After the hit was released, the
breakpoint deleted, and the instance settled, the same override answered 409
again, naming the same two states.

RESULT: PASS
