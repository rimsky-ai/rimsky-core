---
story: claim-producer-observability
status: as-is
---

# Operator dashboards producer-side state

## Role

As an operator running a dashboard against a rimsky deployment, I can fetch a claim's full detail, stream live claim-state changes, paginate the producer's claim inventory, and render custom admin views the producer declares, so that I see producer-side state without writing a custom backplane.

## Capability

Claim-producer observability protocol: claim-detail fetch; live claim-state stream; claim-inventory pagination; producer-declared admin views surfaced through the dashboard route.

## Business value

Operators see producer-side state through rimsky's standard dashboard route — without writing a custom backplane for every producer.

## Acceptance

With a producer advertising the claim-producer-observability protocol, the operator's dashboard queries claim detail and receives the producer's actual state for that claim; subscribes to the live stream and observes state transitions as they happen; paginates the producer's claim inventory; renders an admin view the producer declared, with data from the producer.

## Falsifier

Streamed claim state lags or drops, OR an admin view the producer declared isn't surfaced through the dashboard route, OR the inventory pagination synthesizes rows.

## Proof

Executable proof.

## Notes

2026-06-08 — Story landed via spec 2026-06-08-design-corpus-bootstrap.
