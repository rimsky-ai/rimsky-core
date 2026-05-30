---
concept: message
status: as-is
aliases: []
---

# Message

## Definition

A boundary-crossing dispatch unit. Pushed envelope matched via subscription. Persisted in a message ledger on receipt; delivered to subscribers at frame boundary per the per-instance `FrameDeliveryMode` (`serial_queue` default; `coalesce` opt-in).

`FrameDeliveryMode` (the per-instance message-**delivery** knob, persisted on the instance) is distinct from `FrameResolutionMode` (the template-driven frame-**aggregation** knob owned by `concept:frame`). They share the `serial_queue` / `coalesce` value names but govern different things; this concept owns only the delivery knob.

Envelope shape:

| Field | Required | Notes |
|---|---|---|
| `id` | yes | UUID; rimsky-assigned |
| `instance_id` | yes | target instance |
| `kind` | yes | V1: `invalidate` only |
| `sender` | yes | identity of the sender (`operator`; publisher name like `sensor-cron`; future `instance:<id>`) |
| `sender_kind` | yes | `operator | publisher | instance` |
| `target` | optional | node alias in the receiving instance |
| `payload` | optional | opaque bytes; inert per discipline (`@blessed-invariant 24`) |
| `received_at` | yes | rimsky-assigned timestamp |

## Idempotency

The message-emit endpoint accepts an `Idempotency-Key` HTTP header (string, ≤256 chars). When present, rimsky computes the dedup tuple `(instance_id, sender, idempotency_key)` and INSERTs into a dedup ledger. On unique-key conflict, the handler returns the previously-recorded `message_id` with `200 OK` (rather than `201 Created`) — the response body shape is identical, status code is the only signal of replay. Dedup records expire on a configurable trailing window (default 24h) swept under the scheduler-tick advisory lock.

The idempotency feature is universal — operator retries, publisher emissions, lifecycle handlers all use the same `Idempotency-Key` header. Bundled publishers generate keys per fire (cron: `{subscription_id}+{fire_window_iso}`; http: `{subscription_id}+{body_sha256}`; object-store: `{subscription_id}+{object_etag}`; webhook: `{subscription_id}+{idempotency_header_value}`).

## Boundaries

Owns: the message envelope shape, the message ledger, the per-instance `FrameDeliveryMode` and its delivery semantics (`serial_queue` default vs `coalesce` opt-in), the subscription-walk at frame boundary, the dead-letter audit, the universal dedup ledger. Does NOT own: cascade walks (in-frame; not messages — see `concept:cascade`), event emissions (executor-internal; see `concept:named-event`), the frame creation mechanics and the template-driven `FrameResolutionMode` (frame aggregation — see `concept:frame`). Adjacent: `concept:frame`, `concept:node-subscription`, `concept:publisher`, `concept:publisher-subscription`, `concept:sensor`, `concept:invalidate` (one `kind` of message in V1), `concept:backfill`.

## Invariants

- Two emit sites for V1: operator API (the message-emit endpoint with `sender_kind: "operator"`) and publisher emissions (the same endpoint with `sender_kind: "publisher"` + a publisher-subscription capability token). Cascade walks within a frame are NOT messages — they are direct stale-marks inside the frame.
- Delivery at frame boundary: pending messages match against the target instance's subscriptions (kind, sender, sender_kind, target); matched subscribers' nodes are stale-marked in the new frame; the message's `delivered_at` and `frame_id` populate. Multiple matching subscribers fire in the same frame.
- Per-instance `FrameDeliveryMode`: `serial_queue` (**default**) delivers the oldest one message per frame and leaves the rest pending until the next frame — so each message gets its own frame, processed in received-order, which is unambiguous and the intuitive default. `coalesce` (opt-in) delivers pending messages in strict received-order, coalescing until a message would resolve a node's substitution to a **conflicting** (different) value, then breaks into the next frame; same-value bindings are idempotent and coalesce freely. Only a genuine value-disagreement breaks the frame.
- If no matching subscriber, message dead-lettered (audited in the message ledger with `delivered_at` set, no firings recorded). Visible via the operator message-tail surface.
- Payload is inert per `@blessed-invariant 24`. Read only at the substitution leaf and the persistence-layer fetch.
- Publisher requests are capability-checked: rimsky validates that the publisher-subscription is a live, active binding for the target instance before insert; mismatch returns a forbidden response. The request's `sender` field is ignored — rimsky derives `sender` from the publisher-subscription's publisher name.

## Notes

Introduced by `spec:2026-05-15-data-platform-extensions-design`. The 2026-05-17 publisher unification (`spec:2026-05-17-sensor-messaging-unification-design`) collapses what was previously a special observation-deposit route into the generic message-emit endpoint: bundled sensors now POST standard envelopes to that endpoint with `sender_kind: "publisher"` instead of a sensor-specific deposit endpoint. Plus the universal idempotency-key header lands here.

- 2026-05-23 — Per `spec:2026-05-23-signal-taxonomy-and-policy-decoupling-design`: under `concept:signal`'s field-naming convention, the message envelope's `payload` field is exposed to CEL subscription `when:` predicates as `payload.message_payload` (rather than `payload.payload`) to avoid colliding with the signal envelope's outer `payload` field. The substitution surface (`{{trigger.message.payload.X}}`) is NOT renamed — substitution does not have the envelope-collision problem since it goes through the explicit `trigger.message.` namespace prefix. This deliberate asymmetry keeps substitution backward-compatible and confines the rename to where it's structurally required.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
- 2026-05-29 — Per `spec:2026-05-29-console-upstream-auth-audit-and-fixes`, two changes to `FrameDeliveryMode` (per-instance message delivery — explicitly **not** `FrameResolutionMode`, the template-driven frame-aggregation knob owned by `concept:frame`, which is unchanged): (1) the default flips from `coalesce` to **`serial_queue`** (one message per frame; the intuitive default — each backfill is its own frame/rerun/override; coalesce becomes the opt-in mode); (2) `coalesce` becomes **conflict-aware** — it delivers in received-order until a message would resolve a node's substitution to a conflicting (different) value, then breaks the frame, instead of coalescing all pending messages and colliding distinct overrides. Updated the Definition, Boundaries, and Invariants spots that previously asserted coalesce-default / coalesce-delivers-all. This is what makes backfill partition-request overrides land unambiguously (see `concept:backfill`, `concept:fan-out`).
