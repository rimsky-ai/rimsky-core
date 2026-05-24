---
concept: message
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
  - ../../specs/2026-05-17-sensor-messaging-unification-design.md
---

# Message

## Definition

A boundary-crossing dispatch unit. Pushed envelope matched via subscription. Persisted in `rimsky_messages` on receipt; delivered to subscribers at frame boundary per the per-instance `frame_delivery_mode` (`coalesce` default; `serial_queue` opt-in).

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

`route:POST /instances/{id}/messages` accepts an `Idempotency-Key` HTTP header (string, ≤256 chars). When present, rimsky computes the dedup tuple `(instance_id, sender, idempotency_key)` and INSERTs into `table:rimsky_message_idempotencies`. On unique-key conflict, the handler returns the previously-recorded `message_id` with `200 OK` (rather than `201 Created`) — the response body shape is identical, status code is the only signal of replay. Dedup rows expire on a configurable trailing window (default 24h) swept by `code:runtime/sweep_message_idempotencies.go` under the scheduler-tick advisory lock.

The idempotency feature is universal — operator retries, publisher emissions, lifecycle handlers all use the same `Idempotency-Key` header. Bundled publishers generate keys per fire (cron: `{subscription_id}+{fire_window_iso}`; http: `{subscription_id}+{body_sha256}`; object-store: `{subscription_id}+{object_etag}`; webhook: `{subscription_id}+{idempotency_header_value}`).

## Boundaries

Owns: the message envelope shape, the `rimsky_messages` table, the delivery semantics (coalesce vs serial_queue), the subscription-walk at frame boundary, the dead-letter audit, the universal `rimsky_message_idempotencies` dedup table. Does NOT own: cascade walks (in-frame; not messages — see `concept:cascade`), event emissions (executor-internal; see `concept:named-event`), the frame creation mechanics (see `concept:frame`). Adjacent: `concept:frame`, `concept:node-subscription`, `concept:publisher`, `concept:publisher-subscription`, `concept:sensor`, `concept:invalidate` (one `kind` of message in V1), `concept:backfill`.

## Invariants

- Two emit sites for V1: operator API (`POST /instances/{id}/messages` with `sender_kind: "operator"`) and publisher emissions (`POST /instances/{id}/messages` with `sender_kind: "publisher"` + `publisher_subscription_id` capability token). Cascade walks within a frame are NOT messages — they are direct stale-marks inside the frame.
- Delivery at frame boundary: pending messages match against the target instance's subscriptions (kind, sender, sender_kind, target); matched subscribers' nodes are stale-marked in the new frame; the message's `delivered_at` and `frame_id` populate. Multiple matching subscribers fire in the same frame.
- Per-instance `frame_delivery_mode`: `coalesce` (default) delivers all pending messages into one frame; `serial_queue` delivers the oldest one only and leaves the rest pending until the next frame.
- If no matching subscriber, message dead-lettered (audited in `rimsky_messages` with `delivered_at` set, no firings recorded). Visible via `rimsky-cli messages tail`.
- Payload is inert per `@blessed-invariant 24`. Read only at the substitution leaf (`graph/attribute/substitution.go::resolveTrigger` via `walkPath`) and the persistence-layer fetch (`control/controlapi/messages.go::handleGetMessage`).
- Publisher requests are capability-checked: rimsky validates `(publisher_subscription_id, instance_id, state='active')` is a live row in `rimsky_publisher_subscriptions` before insert; mismatch returns `403 Forbidden`. The request's `sender` field is ignored — rimsky derives `sender` from the row's `publisher_name`.

## Annotation sites

- `code:runtime/message_delivery.go` — enqueue + frame-boundary delivery.
- `code:foundation/persistence/messages.go` — table-shape interface.
- `code:foundation/persistence/message_idempotencies.go` — idempotency dedup interface.
- `code:control/controlapi/messages.go::handleCreateMessage` — universal message-create handler (idempotency + capability check).
- `code:graph/attribute/substitution.go::resolveTrigger` — `{{trigger.message.payload.X}}` substitution.
- `code:runtime/sweep_message_idempotencies.go` — retention sweep.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The 2026-05-17 publisher unification (`.ok-planner/specs/2026-05-17-sensor-messaging-unification-design.md`) collapses what was previously a special observation-deposit route into the generic messages endpoint: bundled sensors now POST standard envelopes to `/instances/{id}/messages` with `sender_kind: "publisher"` instead of a sensor-specific deposit endpoint. Plus the universal idempotency-key header lands here.

- 2026-05-23 — Per spec `.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md`: under `concept:signal`'s field-naming convention, the message envelope's `payload` field is exposed to CEL subscription `when:` predicates as `payload.message_payload` (rather than `payload.payload`) to avoid colliding with the signal envelope's outer `payload` field. The substitution surface (`{{trigger.message.payload.X}}`) is NOT renamed — substitution does not have the envelope-collision problem since it goes through the explicit `trigger.message.` namespace prefix. This deliberate asymmetry keeps substitution backward-compatible and confines the rename to where it's structurally required.
