---
decision: deposit-detection-watermark
status: as-is
---

# Deposit detection is polling with a durable watermark, at-least-once

## Choice

Deposits are detected by periodically listing the watched location and comparing against a durable per-subscription watermark and seen-set, persisted in the sensor's state store. Each discovery publishes with an idempotency key derived from subscription, object name, and content etag; a failed publish does not advance the watermark, so delivery is at-least-once with downstream dedup by key.

## Rationale

Listing is the lowest common denominator every storage technology offers, so polling is the only detection that works uniformly across backends — which is what keeps the single-abstraction choice honest (see also: object-store-watching-model). The durable watermark is what turns polling into a promise: restarts do not re-trigger consumed deposits, and content deposited during downtime is caught on the next poll rather than lost. At-least-once with idempotent keys puts the dedup burden where it is cheap (a key comparison downstream) instead of where it is expensive (transactional exactly-once in the sensor).

## Alternatives

- Stateless polling — trivially simple, but every restart re-triggers the world.
- Exactly-once delivery via a transactional outbox in the sensor — heavier machinery for a guarantee downstream idempotency already provides.
- Operating-system filesystem-change notification for the filesystem backend, with bucket-notification analogs for object stores — the mechanisms are per-backend, are undelivered or lossy across exactly the deployment boundaries this sensor crosses (network filesystems, containerized watchers on bind-mounted host directories), and still require a reconciling scan to be correct against dropped events — so polling is the load-bearing mechanism either way, and hooks would only buy latency the story does not need.
