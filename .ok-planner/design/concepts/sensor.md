---
concept: sensor
status: as-is
aliases: []
---

# Sensor

## Definition

A sensor is a class of `concept:publisher` implementation that observes external state. Sensors poll, listen, or otherwise watch some out-of-rimsky substrate (clock, HTTP endpoint, object-store prefix, webhook port) and publish messages into rimsky when the watched substrate changes.

Sensors implement the `concept:publisher` protocol — a capabilities handshake, subscribe, unsubscribe, and list-subscriptions — and POST message envelopes to the generic operator message-emit endpoint with `sender_kind: "publisher"` + `publisher_subscription_id` capability token.

The bundled reference impls (the cron, HTTP, object-store, and webhook sensors) are sensors-by-construction; they share no protocol-level surface with rimsky beyond the Publisher protocol itself.

## Purpose

To bridge external substrate changes into rimsky's instance frames without requiring rimsky-core knowledge of the substrate. A sensor observes the substrate, builds an opaque payload, and hands it to rimsky as a generic `concept:message`; rimsky routes the message through the existing cascade machinery.

## Boundaries

Owns: the watching loop, the substrate dialect (cron expression, HTTP poll, object-store list), the in-binary per-subscription state (next fire time, body hash, watermark cursor, last idempotency key), and the message-envelope construction at fire time.

Does NOT own: the wire protocol (that's `concept:publisher`), the message envelope shape (that's `concept:message`), the per-instance binding state (that's `concept:publisher-subscription`, stored in the rimsky-side publisher-subscription ledger), or the deployment-tier replica posture (that's `concept:replica`).

Adjacent: `concept:publisher` (sensors implement it), `concept:publisher-subscription` (sensors hold its publisher-side state in their own per-binary state DB), `concept:message` (sensors emit them), `concept:replica` (sensor binaries are single-replica per v1 contract).

## Invariants

- Sensors are deployed as standalone services advertised in the publishers block of the unified config (see `concept:rimsky-yml`). Same deployment model as `concept:claim-producer` or `concept:executor`.
- Templates declare sensors via the publishers block (the same block; sensors ARE publishers); at instance creation, rimsky resolves each publisher entry's config via `{{params.X}}` substitution and calls the publisher protocol's subscribe verb.
- At instance termination, rimsky calls the publisher protocol's unsubscribe verb for each registered publisher-subscription.
- Each emit constructs a message envelope `{kind, target, payload, sender, sender_kind: "publisher", publisher_subscription_id}` and POSTs it to the operator message-emit endpoint with an idempotency-key header. Inert payload per `@blessed-invariant: messages are inert in rimsky`.
- Sensors observe; they do not interpret. Payload bytes flow through rimsky unread until a consumer's substitution leaf walks into them.
- Single-replica per `concept:replica` — operators run one pod per sensor binary; rimsky does not coordinate multi-replica fan-in.
