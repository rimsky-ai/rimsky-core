---
concept: invalidate
status: as-is
aliases: []
---

# Invalidate

## What it is

`invalidate` is the sole graph-level message that the scheduler / control-api emits to mark a node `stale`. There is exactly one **template-configurable** emit site: the operator API's instance-invalidate endpoint. Runtime-internal emitters (scheduler-tick, cascade-walk from subscription-edge matches) are not template-configurable.

## Purpose

Rimsky deliberately ships exactly one inter-node message. The richer behaviors (recalculate, retry, parked-wake) are derived from this one message plus scheduler-side rules. This keeps the runtime's effective vocabulary small.

## Boundaries

Owns: the message itself, the single template-configurable emit site (operator API), the `frame: in | next` discipline. Does NOT own: cascade firing (see `cascade`), scheduled/sensor-driven fires (see `sensor`), frame creation (see `frame`). Adjacent: `frame`, `cascade`, `error-policy`, `parked-state` (admin-invalidate also wakes parked nodes).

## Invariants

- Only one graph-level message exists: `invalidate` (recalculation is a scheduler action, not a service message).
- Operator-originated invalidates do not preempt running work; they enqueue or coalesce into a frame.
- `frame: in | next` default is `next`. The sole template-configurable emit site is the operator API; runtime-internal emitters (scheduler-tick, cascade-walk from subscription-edge matches) are not template-configurable. An operator `frame: in` request resolves the target instance's currently-running frame and joins it (marks the target stale with that frame_id); when no frame is currently running it falls back deterministically to next-frame. This is the documented exception to "operator-API messages always create a new frame": an explicit `frame: in` join is permitted into an open running frame.
