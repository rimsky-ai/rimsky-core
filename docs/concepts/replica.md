# Replica

A replica is one running pod/process of a rimsky-platform binary, behind a deployment-tier load-balancing layer. Replicas are a deployment-tier concern; rimsky's runtime does not model replicas as a first-class concept.

## V1 replica posture by binary

- `rimsky-control-api`, `rimsky-supervisor`, `rimsky-scheduler` — N replicas; cross-replica coordination through claim-handle and scheduler-tick advisory locks.
- `executor-*` — N replicas behind a load balancer; rimsky dispatch picks any reachable replica via gRPC pick_first.
- `stores/*` — depends on the store; typically single-replica per producer name.
- `sensors/sensor-*` (publishers) — **single-replica per binary**. Two replicas of the same sensor binary pointed at the same rimsky endpoint will double-fire (each replica receives identical Subscribe calls and fires independently). Operators wanting HA pick a publisher implementation that handles it; the v1 contract for bundled sensors is single-replica plus restart-on-fail recovery.

## Why bundled sensors are single-replica

The cost-benefit analysis ran the other way: an advisory-lock primitive per publisher-subscription would solve the double-fire problem at the cost of additional rimsky-runtime coordination logic and a more complex publisher SPI. With single-replica + state persistence (sensors-http / -object-store / -webhook persist their state across restarts), the failure mode reduces to "one missed fire window during restart" — acceptable for the invalidation-freshness use case rimsky targets.

See also: [publisher](publisher.md), [sensor](sensor.md).
