---
concept: replica
status: as-is
aliases: []
references:
  - ../../specs/2026-05-17-sensor-messaging-unification-design.md
---

# Replica

## Definition

A replica is one running pod/process of a rimsky-platform binary, behind a deployment-tier load-balancing layer. Replicas are a deployment-tier concern; rimsky's runtime does not model replicas as a first-class concept. When operators scale a binary horizontally, rimsky-level behavior at scale=N is the union of N independent processes; replica-aware coordination (mutex per work-item, leader election, sticky routing) is not a service rimsky provides.

## Purpose

To document that scaling rimsky binaries horizontally is the operator's decision and the operator's responsibility, and that the platform itself takes no opinion on replica count beyond what individual binaries require for correctness.

## Boundaries

Owns: the design statement "rimsky doesn't model replicas." That's it.

Does NOT own: the actual replica posture of any individual binary, the deployment-tier load balancer config, the operator's scaling decisions, or any per-binary HA semantics. Adjacent: `concept:supervisor` (where the actual coordination primitives live — advisory locks, heartbeats), `concept:executor` (executors can be replicated freely; rimsky load-balances dispatch among reachable replicas), `concept:publisher` and `concept:sensor` (per their own per-concept replica policies).

## Invariants

- For every binary, the v1 contract documents its replica posture:
  - `pkg:cmd/rimsky-control-api/` — N replicas behind a load balancer; statelessly serves operator-facing routes.
  - `pkg:cmd/rimsky-supervisor/` — N replicas, coordinated through claim-handle / orphan-reap advisory locks.
  - `pkg:cmd/rimsky-scheduler/` — N replicas, coordinated through scheduler-tick advisory lock.
  - `pkg:github.com/fallguyconsulting/rimsky-services/sensors/sensor-*/` — single replica per binary. Each sensor binary's bundled implementation is honestly single-replica; running two sensor-cron replicas pointed at the same rimsky endpoint will double-fire per fire window. Operators wanting HA pick a publisher implementation that handles it.
  - `pkg:github.com/fallguyconsulting/rimsky-services/executors/*` (production-side: `claude-agent`, `http-node`, `verifier-http`, `verifier-shape-checks`) — N replicas behind a load balancer; rimsky dispatch picks any reachable replica. The in-rimsky `pkg:executors/stub` test double inherits the same posture for completeness.
  - `pkg:github.com/fallguyconsulting/rimsky-services/stores/*` (production-side: `filesystem`, `postgres`) — depends on the store; postgres / filesystem stores are typically single-replica. The in-rimsky `pkg:stores/stub` test double is single-process by construction.

- Multi-replica safety (when required) lives in the binary's implementation, not rimsky's runtime. The supervisor's claim-handle advisory lock is the canonical pattern; bundled sensors do NOT attempt similar coordination.

- The control-api routes that depend on cross-replica consistency (subscription routing, message delivery) are coordinated via the underlying persistence layer's atomicity, not via rimsky-level coordination.

## Notes

Introduced by the 2026-05-17 publisher-unification spec to document the v1 sensor replica posture decision. The earlier pre-2026-05-17 draft proposed adding per-publisher-subscription advisory locks to coordinate multi-replica sensors; that proposal was retired in favor of "single-replica is the v1 contract."

If a publisher implementation wants HA, it owns the implementation. Rimsky's job at the protocol surface is "accept messages from publishers and deliver them"; HA at the publisher tier is a sibling concern.

2026-05-24: path references retargeted from in-tree bundled-impl locations to pkg:github.com/fallguyconsulting/rimsky-services. See spec 2026-05-24-repo-reorganization-design phase P3.
