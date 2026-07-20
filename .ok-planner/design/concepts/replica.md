---
concept: replica
status: as-is
aliases: []
---

# Replica

## Definition

A replica is one running pod/process of a rimsky-platform binary, behind a deployment-tier load-balancing layer. Replicas are a deployment-tier concern; rimsky's runtime does not model replicas as a first-class concept, does not detect, heartbeat, or individually address them, and provides no generic replica-aware coordination (mutex per work-item, sticky routing, failover) for horizontally scaled binaries. The one exception is the scheduler's own dispatch tick: rimsky's runtime serializes each tick to a single replica via a pinned per-tick advisory lock (`concept:advisory-lock`), so at scale=N the scheduler role is not simply the union of N independent processes. Every other role — supervisor, executor, publisher, sensor — behaves as N independent processes, coordinating only through mechanisms each binary's own implementation chooses.

The all-in-one deployment collapses every role into a single process — one replica of one process serving every role surface. Per-role replicas are the split deployment's shape, where each role runs as its own process and scales independently.

## Purpose

To document that scaling rimsky binaries horizontally is the operator's decision and the operator's responsibility, and that the platform itself takes no opinion on replica count beyond what individual binaries require for correctness.

## Boundaries

Owns: the design statement "rimsky doesn't model replicas." That's it.

Does NOT own: the actual replica posture of any individual binary, the deployment-tier load balancer config, the operator's scaling decisions, or any per-binary HA semantics. Adjacent: `concept:advisory-lock` (the coordination primitives rimsky's own role binaries use — the scheduler's per-tick lock and the supervisor's claim-handle lock), `concept:executor` (executors can be replicated freely; rimsky resolves each executor name to a single endpoint and dials it, so distributing dispatch across executor replicas is a deployment-tier load-balancer/DNS concern behind that endpoint, not something rimsky itself does), `concept:publisher` and `concept:sensor` (per their own per-concept replica policies).

## Invariants

- Each binary's v1 contract documents its own replica posture. A single-replica publisher implementation that observes a shared substrate will double-fire across replicas; operators wanting HA must pick a publisher implementation that coordinates internally.

- Multi-replica safety across binaries is not a generic service rimsky provides: each role binary owns its own coordination needs. Rimsky's own role binaries draw on the persistence layer's advisory-lock primitives (`concept:advisory-lock`) for the coordination they do need — the scheduler's per-tick lock and the supervisor's claim-handle lock are the two canonical uses. Bundled sensors do NOT attempt similar coordination and honestly fire once per replica per window; externally-authored binaries (publishers, custom sensors, executors) needing HA must coordinate internally, the same way.

- The cross-replica-consistent control-api routes are coordinated via the underlying persistence layer's atomicity, not via rimsky-level coordination.

- Rimsky distinguishes exactly two deployment topologies for its own role binaries — unified (all-in-one, one process) and split (per-role processes, any replica count) — and carries no finer-grained replica-count signal, consistent with not modeling replicas as a first-class concept. The SQLite driver is safe under either topology as long as every role process and every replica shares one local database file (`concept:persistence-database`); choosing SQLite outside the unified topology logs an informational warning naming that precondition rather than blocking startup, since rimsky cannot itself verify the deployment's filesystem layout.
