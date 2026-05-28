---
concept: invalidate
status: as-is
aliases: []
---

# Invalidate

## What it is

`invalidate` is the sole graph-level message that the scheduler / control-api emits to mark a node `stale`. Post-2026-05-23 there is exactly one **template-configurable** emit site: the operator API's instance-invalidate endpoint. Runtime-internal emitters (scheduler-tick, cascade-walk from subscription-edge matches) are unchanged and remain documented but are not template-configurable. The `error_types:` policy invalidate action and the lifecycle-handler `invalidate:` slot retired (the action was retired 2026-05-14; the now-retired lifecycle-handler concept retires entirely 2026-05-23).

## Purpose

Rimsky deliberately ships exactly one inter-node message. The richer behaviors (recalculate, retry, parked-wake) are derived from this one message plus scheduler-side rules. This keeps the runtime's effective vocabulary small.

## Boundaries

Owns: the message itself, the single template-configurable emit site (operator API), the `frame: in | next` discipline. Does NOT own: cascade firing (see `cascade`), scheduled/sensor-driven fires (see `sensor`), frame creation (see `frame`). Adjacent: `frame`, `cascade`, `error-policy`, `parked-state` (admin-invalidate also wakes parked nodes).

## Invariants

- Only one graph-level message exists: `invalidate` (recalculation is a scheduler action, not a service message).
- Operator-originated invalidates do not preempt running work; they enqueue or coalesce into a frame.
- `frame: in | next` default is `next`. The sole template-configurable emit site is the operator API; runtime-internal emitters (scheduler-tick, cascade-walk from subscription-edge matches) are unchanged.

## Aliases and historical names

The verb "invalidate" replaced an earlier richer message set under the v3 redesign.

## Open within this concept

(no open items distinct from the parent `cascade`/`frame` concepts)

## Notes

- 2026-05-14: emitter list updated. Operator API, scheduler tick, and the cascade walk from subscription-edge matches remain as emitters. The error-types policy's `action: invalidate` and lifecycle-handler `invalidate.targets:` are retired; their effects are now declared as receiver-side subscriptions (see `concept:node-subscription`). Per `spec:2026-05-14-subscription-cascade-and-quality-of-life`.
- 2026-05-15: **`invalidate` is one `kind` of message (the V1 kind)**. The boundary-crossing messaging primitive is `concept:message`; it has a `kind` field that in V1 carries only the value `invalidate`. Both operator-API sends (`sender_kind: "operator"`) and publisher emissions (`sender_kind: "publisher"` per the 2026-05-17 unification — bundled sensors are publishers) hit the universal instance message-emit endpoint, construct invalidate-kind messages, and enqueue them in the message ledger; the cascade walk (in-frame subscription-edge match) is NOT a message — it's a direct stale-mark inside the frame. The retired per-emit `frame: in | next` discipline is subsumed by message-vs-cascade distinction: messages always create a new frame (or join the pending coalesce row); cascade walks always run within the current frame. See `concept:message`, `concept:frame`, `concept:backfill` (backfills are invalidate-kind messages with a `partition_request_override` payload).
- 2026-05-23 — Template-configurable emit-site enumeration collapses from three to one (operator-API). Runtime-internal emitters (scheduler-tick, cascade-walk from subscription-edge matches) are unchanged. The error_types policy invalidate site and the lifecycle-handler invalidate slot stop existing under `spec:2026-05-23-signal-taxonomy-and-policy-decoupling` (the action was retired 2026-05-14; the now-retired lifecycle-handler concept retires entirely).
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
