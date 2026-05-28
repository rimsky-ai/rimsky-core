---
concept: publisher
status: as-is
aliases: []
---

# Publisher

## Definition

A publisher is a peer service that publishes messages into rimsky. Publishers implement the publisher protocol (four verbs: a capabilities handshake, subscribe, unsubscribe, and list-subscriptions) and POST message envelopes to the generic operator message-emit endpoint with `sender_kind: "publisher"` and a `publisher_subscription_id` capability token.

Publishers are peer-services in the same trust perimeter as executors and claim-producers: out-of-process, gRPC-addressed at startup via the publishers block of the unified config (see `concept:rimsky-yml`), and exclusively responsible for their own state and HA posture.

## Purpose

To give rimsky a uniform way to accept inbound messages from peer services — sensors, schedulers, change-data-capture pipes — without each implementation needing its own bespoke deposit route. The publisher protocol is the single message-emit surface for peer services; operators only ever fire messages via the universal message-emit endpoint.

## Boundaries

Owns: the four-verb protocol surface (the wire message shapes + RPC contract), the gRPC peer client, the rimsky-side dispatch helpers, the operator-side dial path, and the universal capability check on the messages endpoint.

Does NOT own: the publisher's substrate (cron clock, HTTP endpoint, object-store, etc.), per-publisher state persistence (each publisher owns its own state DB; see `concept:sensor`), the message envelope shape (that's `concept:message`), or the deployment-tier replica posture (that's `concept:replica`).

Adjacent: `concept:publisher-subscription` (the rimsky↔publisher binding lifecycle), `concept:sensor` (one class of publisher implementation), `concept:message` (the envelope shape), `concept:claim-producer` and `concept:executor` (peer-service siblings with their own protocols), `concept:replica` (publisher replica posture).

## Invariants

- Publishers are advertised under the top-level publishers block of the unified config (see `concept:rimsky-yml`). Their declared protocol list must include `"publisher"`.
- The subscribe verb carries inline routing fields (`target_node`, `message_kind`); there is no `on_change` substruct. The publisher persists these fields and copies them onto each emitted message envelope.
- Emit-time messages set `sender_kind: "publisher"` and `publisher_subscription_id`. Rimsky derives `sender` from the publisher-subscription row's `publisher_name`; the request's `sender` is ignored for trust.
- Rimsky retries the subscribe verb up to 3 times with exponential backoff (200ms → ~560ms → ~1.6s, ±25% jitter) before flipping the publisher-subscription row to `state='failed'`.
- Replicas are not coordinated by rimsky. Single-replica is the v1 contract per `concept:replica`.
- @blessed-invariant: messages are inert in rimsky. Payload bytes flow from publisher → message envelope → consumer's substitution leaf without inspection.

## Notes

The protocol is the 2026-05-17 rename of what was previously called the sensor protocol. The reframe: rimsky's wire-level abstraction is "a peer that publishes messages"; sensors are one kind of publisher. The earlier name baked the implementation class into the protocol name. The new name is honest about what the protocol is.

The bundled implementations keep their sensor-named identities — they ARE sensors at the binary boundary, but at the wire boundary their protocol role is publisher (see `concept:sensor`).

2026-05-24: calling-side gRPC client path renamed and bundled-sensor implementations relocated to the consumption side, outside the platform, per `spec:2026-05-24-repo-reorganization-design` (phases P2 and P3).
2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
