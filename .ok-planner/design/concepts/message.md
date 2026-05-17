---
concept: message
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
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
| `sender` | yes | identity of the sender (`operator`; sensor name including `sensor-cron`; future `instance:<id>`) |
| `sender_kind` | yes | `operator | sensor | instance` |
| `target` | optional | node alias in the receiving instance |
| `payload` | optional | opaque bytes; inert per discipline (`@blessed-invariant 24`) |
| `received_at` | yes | rimsky-assigned timestamp |

## Boundaries

Owns: the message envelope shape, the `rimsky_messages` table, the delivery semantics (coalesce vs serial_queue), the subscription-walk at frame boundary, the dead-letter audit. Does NOT own: cascade walks (in-frame; not messages — see `concept:cascade`), event emissions (executor-internal; see `concept:named-event`), the frame creation mechanics (see `concept:frame`). Adjacent: `concept:frame`, `concept:subscription`, `concept:sensor`, `concept:invalidate` (one `kind` of message in V1), `concept:backfill`.

## Invariants

- Three emit sites for V1: operator API (`POST /instances/{id}/messages`), sensor observations (`POST /sensors/{watch_id}/observations`), future instance-to-instance. Cascade walks within a frame are NOT messages — they are direct stale-marks inside the frame.
- Delivery at frame boundary: pending messages match against the target instance's subscriptions (kind, sender, sender_kind, target); matched subscribers' nodes are stale-marked in the new frame; the message's `delivered_at` and `frame_id` populate. Multiple matching subscribers fire in the same frame.
- Per-instance `frame_delivery_mode`: `coalesce` (default) delivers all pending messages into one frame; `serial_queue` delivers the oldest one only and leaves the rest pending until the next frame.
- If no matching subscriber, message dead-lettered (audited in `rimsky_messages` with `delivered_at` set, no firings recorded). Visible via `rimsky-cli messages tail`.
- Payload is inert per `@blessed-invariant 24`. Read only at the substitution leaf (`graph/attribute/substitution.go::resolveTrigger` via `walkPath`) and the persistence-layer fetch (`control/controlapi/messages.go::handleGetMessage`).

## Annotation sites

- `code:runtime/message_delivery.go` — enqueue + frame-boundary delivery.
- `code:foundation/persistence/messages.go` — table-shape interface.
- `code:control/controlapi/messages.go` — `POST /instances/{id}/messages`, `GET /messages/{id}`.
- `code:control/controlapi/sensors.go` — `POST /sensors/{watch_id}/observations` (constructs sensor-origin messages).
- `code:graph/attribute/substitution.go::resolveTrigger` — `{{trigger.message.payload.X}}` substitution.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The message primitive unifies what were previously distinct emit sites (operator invalidate, schedule fire, sensor observation, backfill) into one envelope-matching layer. Backfills are messages with a `partition_request_override` payload (see `concept:backfill`). The retired per-emit `frame: in | next` discipline is subsumed by message-vs-cascade distinction.
