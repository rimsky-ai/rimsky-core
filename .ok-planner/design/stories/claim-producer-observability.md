---
story: claim-producer-observability
status: as-is
---

# Operator dashboards producer-side state

## Story

As an operator running a dashboard against a rimsky deployment, I can fetch a claim's full detail, stream live claim-state changes, paginate the producer's claim inventory, and render custom admin views the producer declares, so that I see producer-side state without writing a custom backplane.

Claim-producer observability protocol: claim-detail fetch; live claim-state stream; claim-inventory pagination; producer-declared admin views surfaced through the dashboard route.

Operators see producer-side state through rimsky's standard dashboard route — without writing a custom backplane for every producer.
