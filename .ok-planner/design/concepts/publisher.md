---
concept: publisher
status: as-is
aliases: []
references:
  - ../../specs/2026-05-17-sensor-messaging-unification-design.md
---

# Publisher

## Definition

A publisher is a peer service that publishes messages into rimsky. Publishers implement the `proto:publisher.proto::Publisher` protocol (four verbs: `Capabilities`, `Subscribe`, `Unsubscribe`, `ListSubscriptions`) and POST message envelopes to the generic `route:POST /instances/{id}/messages` endpoint with `sender_kind: "publisher"` and a `publisher_subscription_id` capability token.

Publishers are peer-services in the same trust perimeter as executors and claim-producers: out-of-process, gRPC-addressed at startup via the `publishers:` block of `cfg:rimsky.yml`, and exclusively responsible for their own state and HA posture.

## Purpose

To give rimsky a uniform way to accept inbound messages from peer services — sensors, schedulers, change-data-capture pipes — without each implementation needing its own bespoke deposit route. The publisher protocol is the single message-emit surface for peer services; operators only ever fire messages via `POST /instances/{id}/messages`.

## Boundaries

Owns: the four-verb protocol surface (proto types + RPC contract), the wire-shape Go types under `code:runtime/clientiface/publisher.go`, the gRPC client under `code:runtime/remote/publisher_client.go`, the rimsky-side dispatch helpers under `code:runtime/publishers.go`, the dial path under `code:control/config/publishers.go`, and the universal capability check on the messages endpoint.

Does NOT own: the publisher's substrate (cron clock, HTTP endpoint, object-store, etc.), per-publisher state persistence (each publisher owns its own state DB; see `concept:sensor`), the message envelope shape (that's `concept:message`), or the deployment-tier replica posture (that's `concept:replica`).

Adjacent: `concept:publisher-subscription` (the rimsky↔publisher binding lifecycle), `concept:sensor` (one class of publisher implementation), `concept:message` (the envelope shape), `concept:claim-producer` and `concept:executor` (peer-service siblings with their own protocols), `concept:replica` (publisher replica posture).

## Invariants

- Publishers are advertised under the top-level `publishers:` block of `cfg:rimsky.yml`. Their `protocols:` list must include `"publisher"`.
- Subscribe carries inline routing fields (`target_node`, `message_kind`); there is no `on_change` substruct. The publisher persists these fields and copies them onto each emitted message envelope.
- Emit-time messages set `sender_kind: "publisher"` and `publisher_subscription_id`. Rimsky derives `sender` from the publisher-subscription row's `publisher_name`; the request's `sender` is ignored for trust.
- Rimsky retries the Subscribe RPC up to 3 times with exponential backoff (200ms → ~560ms → ~1.6s, ±25% jitter) before flipping the publisher-subscription row to `state='failed'`.
- Replicas are not coordinated by rimsky. Single-replica is the v1 contract per `concept:replica`.
- @blessed-invariant: messages are inert in rimsky. Payload bytes flow from publisher → message envelope → consumer's substitution leaf without inspection.

## Annotation sites

- `code:protocols/proto/v1/publisher.proto` — protobuf surface.
- `code:runtime/clientiface/publisher.go` — Go-side wire types.
- `code:runtime/remote/publisher_client.go` — gRPC remote client.
- `code:runtime/publishers.go` — rimsky-side lifecycle dispatch (Start / Stop / Resync helpers).
- `code:control/config/publishers.go` — operator-side dial path.
- `code:cmd/rimsky-publisher-conformance/` — conformance suite.

## Notes

The protocol is the 2026-05-17 rename of what was previously called the `Sensor` protocol. The reframe: rimsky's wire-level abstraction is "a peer that publishes messages"; sensors are one kind of publisher. The earlier name baked the implementation class into the protocol name. The new name is honest about what the protocol is.

The bundled implementations under `pkg:sensors/sensor-*/` keep their sensor-named identities — they ARE sensors at the binary boundary, but at the wire boundary their protocol role is publisher.
