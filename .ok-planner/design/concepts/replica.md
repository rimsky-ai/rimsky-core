---
concept: replica
status: as-is
aliases: []
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
  - The control-api binary — N replicas behind a load balancer; statelessly serves operator-facing routes.
  - The supervisor binary — N replicas, coordinated through claim-handle / orphan-reap advisory locks.
  - The scheduler binary — N replicas, coordinated through a scheduler-tick advisory lock.
  - Bundled sensor binaries — single replica per binary. Each sensor binary's bundled implementation is honestly single-replica; running two cron-sensor replicas pointed at the same rimsky endpoint will double-fire per fire window. Operators wanting HA pick a publisher implementation that handles it.
  - Bundled executor binaries (the agent, HTTP-node, and verifier reference implementations) — N replicas behind a load balancer; rimsky dispatch picks any reachable replica. The in-rimsky stub executor test double inherits the same posture for completeness.
  - Bundled store binaries (the filesystem and postgres reference implementations) — depends on the store; postgres / filesystem stores are typically single-replica. The in-rimsky stub store test double is single-process by construction.

- Multi-replica safety (when required) lives in the binary's implementation, not rimsky's runtime. The supervisor's claim-handle advisory lock is the canonical pattern; bundled sensors do NOT attempt similar coordination.

- The control-api routes that depend on cross-replica consistency (subscription routing, message delivery) are coordinated via the underlying persistence layer's atomicity, not via rimsky-level coordination.
