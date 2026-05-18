---
concept: invalidate
status: as-is
aliases: []
references:
  - _discover/2026-05-10-frame-resolution-model.md
  - _discover/2026-05-10-cascade-fires-on-last-outcome.md
  - _discover/reactive-loops-and-lifecycle-handlers.md
---

# Invalidate

## What it is

`invalidate` is the sole graph-level message that the scheduler / control-api / handler emits to mark a node `stale`. There are three emit sites: operator API (`POST /admin/instances/.../invalidate`), error-types policy invalidate (`error_types:` block), and lifecycle-handler `invalidate:` slot. Each carries the target node-types (or `self`) and a `frame: in | next` setting (default `next`).

## Purpose

Rimsky deliberately ships exactly one inter-node message. The richer behaviors (recalculate, retry, parked-wake) are derived from this one message plus scheduler-side rules. This keeps the runtime's effective vocabulary small.

## Boundaries

Owns: the message itself, the three emit sites, the `frame: in | next` discipline. Does NOT own: cascade firing (see `cascade`), schedule fires (see `schedule`), frame creation (see `frame`). Adjacent: `frame`, `cascade`, `lifecycle-handler`, `error-policy`, `parked-state` (admin-invalidate also wakes parked nodes).

## Invariants

- Only one graph-level message exists: `invalidate` (recalculation is a scheduler action, not a service message — CLAUDE.md "Vocabulary").
- Operator-originated invalidates do not preempt running work; they enqueue or coalesce into a frame.
- `frame: in | next` default is `next`; the three emit sites configurable in templates are operator-API, error-types policy, lifecycle-handler.

## Aliases and historical names

The verb "invalidate" replaced an earlier richer message set under the v3 redesign.

## Open within this concept

(no open items distinct from the parent `cascade`/`frame` concepts)

## Notes

- 2026-05-14: emitter list updated. Operator API, scheduler tick, and the cascade walk from subscription-edge matches remain as emitters. The error-types policy's `action: invalidate` and lifecycle-handler `invalidate.targets:` are retired; their effects are now declared as receiver-side subscriptions (see `concept:node-subscription`). Per spec `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`.
- 2026-05-15: **`invalidate` is one `kind` of message (the V1 kind)**. The boundary-crossing messaging primitive is `concept:message`; it has a `kind` field that in V1 carries only the value `invalidate`. Both operator-API sends (`POST /instances/{id}/messages` with `sender_kind: "operator"`) and publisher emissions (`POST /instances/{id}/messages` with `sender_kind: "publisher"` per the 2026-05-17 unification — bundled sensors are publishers) construct invalidate-kind messages and enqueue them in `rimsky_messages`; the cascade walk (in-frame subscription-edge match) is NOT a message — it's a direct stale-mark inside the frame. The retired per-emit `frame: in | next` discipline is subsumed by message-vs-cascade distinction: messages always create a new frame (or join the pending coalesce row); cascade walks always run within the current frame. See `concept:message`, `concept:frame`, `concept:backfill` (backfills are invalidate-kind messages with a `partition_request_override` payload).
