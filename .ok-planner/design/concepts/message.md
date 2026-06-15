---
concept: message
status: as-is
aliases: []
---

# Message

## Definition

A typed envelope whose arrival at an instance opens a frame. The envelope's `type` field selects an entry from the instance's template message-schema registry; an undeclared type is refused at receipt with an unknown-type response. Persisted in the message ledger on receipt; delivered to subscribers at the next frame boundary, one message per frame. Cascade-emitted, operator-emitted, and publisher-emitted messages traverse the same delivery path.

Envelope shape: `id`, `instance_id`, `type` (the message type-path), `sender`, `sender_kind` (`operator | publisher | instance`), `payload` (the typed body, inert), `received_at`. Receivers are decided by subscription to the message type as a virtual node-type — there is no envelope-side routing field.

## Idempotency

The message-emit endpoint requires an `Idempotency-Key` HTTP header (string, ≤256 chars). Requests without the header are refused. Rimsky computes the dedup tuple `(instance_id, sender_kind, sender, sender_subject, idempotency_key)`, where the dedup-layer `sender_kind` enum is `operator | publisher | anonymous` (see `decision:message-sender-kind-discriminator` for the relationship to the envelope's three-value `sender_kind`). The `sender_subject` column carries the requester's identity (api-key id, publisher subscription id, or the `anonymous` sentinel) so distinct callers with the same key never replay each other. Rimsky INSERTs into a dedup ledger; on unique-key conflict, the handler returns the original `message_id` with `200 OK` (rather than `201 Created`) — the response body shape is identical, status code is the only signal of replay. Dedup records expire on a configurable trailing window (default 24h) swept under the scheduler-tick advisory lock.

The idempotency feature is universal — operator retries, publisher emissions, lifecycle handlers all use the same `Idempotency-Key` header. Bundled publishers generate keys per fire (cron: `{subscription_id}+{fire_window_iso}`; http: `{subscription_id}+{body_sha256}`; object-store: `{subscription_id}+{object_etag}`; webhook: `{subscription_id}+{idempotency_header_value}`).

## Boundaries

Owns: the envelope shape and the message ledger; the one-message-per-frame delivery rule; the subscription-walk-as-virtual-node at frame boundary (each message type is a virtual node-type emitting `terminal/success` on arrival); the dead-letter audit (no-subscriber landings still write a ledger row with a `terminal/success` emission); the universal `Idempotency-Key` dedup ledger; the registry lookup gate on receipt. Does NOT own: the type registry itself (see `concept:message-schema`); cascade walks within a frame (see `concept:cascade`); event emissions from executors (see `concept:named-event`); the frame creation mechanics (see `concept:frame`); the publisher's substrate state (see `concept:publisher` / `concept:publisher-subscription`); the emit-node's dispatch (see `concept:message-emitter-node`). Adjacent: `concept:frame`, `concept:node-subscription`, `concept:publisher`, `concept:publisher-subscription`, `concept:sensor`, `concept:message-schema`, `concept:message-emitter-node`.

## Invariants

- Two external emit sites and one internal: operator API (the message-emit endpoint with `sender_kind: "operator"`), publisher emissions (the same endpoint with `sender_kind: "publisher"` + a publisher-subscription capability token), and cascade-emit (a message-emitter node's dispatch, with `sender_kind: "instance"` + sender `instance:<id>`). All three paths land in the same ledger and follow the same delivery rules.
- One message per frame. At each frame boundary, exactly one pending message delivers; the rest stay pending until the next frame.
- Type lookup at receipt: a message whose `type` is not declared in the target template's message-schema registry is refused with an unknown-type response; loud miss, not silent dead-letter.
- Delivery at frame boundary: the message-virtual-node settles in the new frame and emits `terminal/success`; nodes subscribing to that virtual node-type stale-mark; the message's `delivered_at` and `frame_id` populate.
- Payload is inert (see `@blessed-invariant: 21`). Read only at the substitution leaf and the persistence-layer fetch.
- Publisher requests are capability-checked at the existing publisher-subscription validation: rimsky validates that the publisher-subscription is a live, active binding for the target instance.
