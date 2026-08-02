---
concept: publisher
---

# Publisher

## What it is

A publisher is a peer service that publishes messages into rimsky. Publishers implement the publisher protocol (four verbs: a capabilities handshake, subscribe, unsubscribe, and list-subscriptions) and POST message envelopes to the universal operator message-send endpoint, identifying themselves as publishers and presenting a per-subscription capability token.

Publishers are peer-services in the same trust perimeter as executors and claim-producers: out-of-process, addressed at startup via the publisher service registry in `concept:rimsky-yml`, and exclusively responsible for their own state and HA posture.

A publisher service is a provider of broadcasters: one service process serves many instances, and each subscription provisions a logical, per-instance broadcaster within it, parameterized by the instance's resolved config — the per-instance analogue of how an executor provides per-node-run execution.

## Purpose

To give rimsky a uniform way to accept inbound messages from peer services — sensors, schedulers, change-data-capture pipes — without each implementation needing its own bespoke deposit route. The publisher protocol is the single message-send surface for peer services; operators only ever fire messages via the universal message-send endpoint.

## Boundaries

Owns: the protocol surface, the peer client, the rimsky-side dispatch helpers, and the capability check on the universal message-send surface.

Does NOT own: the publisher's substrate (cron clock, HTTP endpoint, object-store, etc.), per-publisher state persistence (each publisher owns its own state DB; see `concept:sensor`), the message envelope shape (that's `concept:message`), or the deployment-tier replica posture (that's `concept:replica`).

Adjacent: `concept:publisher-subscription` (the rimsky↔publisher binding lifecycle), `concept:sensor` (one class of publisher implementation), `concept:message` (the envelope shape), `concept:claim-producer` and `concept:executor` (peer-service siblings with their own protocols), `concept:replica` (publisher replica posture).

## Invariants

- Publishers are advertised in the publisher service registry of `concept:rimsky-yml`. Their declared protocol membership must include the publisher protocol.
- The subscribe verb carries the message type the publisher will stamp on every sent envelope; the subscribe surface carries no receiver-routing field — delivery routes by message type against node-subscription edges. The publisher persists the type and copies it onto each sent message envelope.
- Send-time messages identify the sender as a publisher and present the per-subscription capability token. Rimsky derives the sender name from the publisher-subscription row; the request's declared sender is ignored for trust.
- Mounting-to-active reconciliation, its retry cadence, and the failed-state contract are owned by `concept:publisher-subscription`.
- Replicas are not coordinated by rimsky. Single-replica is the durable posture per `concept:replica`.
- invariant: message-inertness — payload bytes flow from publisher → message envelope → consumer's substitution leaf without inspection.
