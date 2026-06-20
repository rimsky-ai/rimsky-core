---
concept: replica
status: as-is
aliases: []
---

# Replica

## Definition

A replica is one running pod/process of a rimsky-platform binary, behind a deployment-tier load-balancing layer. Replicas are a deployment-tier concern; rimsky's runtime does not model replicas as a first-class concept. When operators scale a binary horizontally, rimsky-level behavior at scale=N is the union of N independent processes; replica-aware coordination (mutex per work-item, leader election, sticky routing) is not a service rimsky provides.

The all-in-one deployment collapses every role into a single process — one replica of one process serving every role surface. Per-role replicas are the split deployment's shape, where each role runs as its own process and scales independently.

## Purpose

To document that scaling rimsky binaries horizontally is the operator's decision and the operator's responsibility, and that the platform itself takes no opinion on replica count beyond what individual binaries require for correctness.

## Boundaries

Owns: the design statement "rimsky doesn't model replicas." That's it.

Does NOT own: the actual replica posture of any individual binary, the deployment-tier load balancer config, the operator's scaling decisions, or any per-binary HA semantics. Adjacent: `concept:supervisor` (where the actual coordination primitives live — advisory locks), `concept:executor` (executors can be replicated freely; rimsky load-balances dispatch among reachable replicas), `concept:publisher` and `concept:sensor` (per their own per-concept replica policies).

## Invariants

- Each binary's v1 contract documents its own replica posture. A single-replica publisher implementation that observes a shared substrate will double-fire across replicas; operators wanting HA must pick a publisher implementation that coordinates internally.

- Multi-replica safety (when required) lives in the binary's implementation, not rimsky's runtime. The supervisor's claim-handle advisory lock is the canonical pattern; bundled sensors do NOT attempt similar coordination.

- The cross-replica-consistent control-api routes are coordinated via the underlying persistence layer's atomicity, not via rimsky-level coordination.
