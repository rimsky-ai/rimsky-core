---
decision: lineage-subscriber-poller
---

# The lineage subscriber polls the durable projection

## Choice

The bundled lineage subscriber emits by periodically polling the durable lineage projection — reading records newer than its cursor on an interval and forwarding them — rather than registering as a push-style lifecycle subscriber.

## Rationale

The projection is durable, so polling is restart-safe and at-least-once by construction: records written while the subscriber is down are picked up on the next tick, with no registration lifecycle to manage and no live-delivery dependency. A push subscriber would still need a reconciling read to cover missed events, so polling is the load-bearing mechanism either way — the same posture the object-store sensor's deposit detection records (see `decision:deposit-detection-watermark`).

## Alternatives

- A push-style lifecycle subscriber registered with rimsky — rejected: adds a registration lifecycle and a live-delivery dependency, and a missed event during downtime still requires a reconciling read against the projection.
