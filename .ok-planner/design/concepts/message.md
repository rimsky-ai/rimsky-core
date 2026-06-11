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
| `sender` | yes | identity of the sender (`operator`; publisher name like `sensor-cron`; `instance:<id>`) |
| `sender_kind` | yes | `operator | publisher | instance` |
| `target` | optional | node alias in the receiving instance |
| `payload` | optional | opaque bytes; inert per discipline (`@blessed-invariant 24`) |
| `received_at` | yes | rimsky-assigned timestamp |

## Idempotency

The message-emit endpoint requires an `Idempotency-Key` HTTP header (string, ≤256 chars). Requests without the header are refused. Rimsky computes the dedup tuple `(instance_id, sender_kind, sender, sender_subject, idempotency_key)`, where the dedup-layer `sender_kind` enum is `operator | publisher | anonymous` (see `decision:message-sender-kind-discriminator` for the relationship to the envelope's three-value `sender_kind`). The `sender_subject` column carries the requester's identity (api-key id, publisher subscription id, or the `anonymous` sentinel) so distinct callers with the same key never replay each other. Rimsky INSERTs into a dedup ledger; on unique-key conflict, the handler returns the original `message_id` with `200 OK` (rather than `201 Created`) — the response body shape is identical, status code is the only signal of replay. Dedup records expire on a configurable trailing window (default 24h) swept under the scheduler-tick advisory lock.

The idempotency feature is universal — operator retries, publisher emissions, lifecycle handlers all use the same `Idempotency-Key` header. Bundled publishers generate keys per fire (cron: `{subscription_id}+{fire_window_iso}`; http: `{subscription_id}+{body_sha256}`; object-store: `{subscription_id}+{object_etag}`; webhook: `{subscription_id}+{idempotency_header_value}`).

## Boundaries

Owns: the message envelope shape, the message ledger, the per-instance `FrameDeliveryMode` and its delivery semantics (`serial_queue` default vs `coalesce` opt-in), the subscription-walk at frame boundary, the dead-letter audit, the universal dedup ledger. Does NOT own: cascade walks (in-frame; not messages — see `concept:cascade`), event emissions (executor-internal; see `concept:named-event`), the frame creation mechanics and the template-driven `FrameResolutionMode` (frame aggregation — see `concept:frame`). Adjacent: `concept:frame`, `concept:node-subscription`, `concept:publisher`, `concept:publisher-subscription`, `concept:sensor`, `concept:invalidate` (one `kind` of message), `concept:backfill`.

## Invariants

- Two emit sites: operator API (the message-emit endpoint with `sender_kind: "operator"`) and publisher emissions (the same endpoint with `sender_kind: "publisher"` + a publisher-subscription capability token). Cascade walks within a frame are NOT messages — they are direct stale-marks inside the frame.
- Delivery at frame boundary: pending messages match against the target instance's subscriptions (kind, sender, sender_kind, target); matched subscribers' nodes are stale-marked in the new frame; the message's `delivered_at` and `frame_id` populate. Multiple matching subscribers fire in the same frame.
- Per-instance `FrameDeliveryMode`: `serial_queue` (**default**) delivers the oldest one message per frame and leaves the rest pending until the next frame — so each message gets its own frame, processed in received-order, which is unambiguous and the intuitive default. `coalesce` (opt-in) delivers pending messages in strict received-order, coalescing until a message would resolve a node's substitution to a **conflicting** (different) value, then breaks into the next frame; same-value bindings are idempotent and coalesce freely. Only a genuine value-disagreement breaks the frame.
- If no matching subscriber, message dead-lettered (audited in the message ledger with `delivered_at` set, no firings recorded). Visible via the operator message-tail surface.
- Payload is inert per `@blessed-invariant 24`. Read only at the substitution leaf and the persistence-layer fetch.
- Publisher requests are capability-checked: rimsky validates that the publisher-subscription is a live, active binding for the target instance before insert; mismatch returns a forbidden response. The request's `sender` field is ignored — rimsky derives `sender` from the publisher-subscription's publisher name.
