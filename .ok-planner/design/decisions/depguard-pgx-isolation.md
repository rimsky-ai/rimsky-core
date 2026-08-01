---
decision: depguard-pgx-isolation
status: as-is
---

# Confine the Postgres driver imports

## Choice

The Postgres driver is imported only by the foundation module's postgres persistence packages, the postgres-backed bundled services, the cmd group, and the test-support and scenario-harness packages; everything else consumes the persistence interfaces. Enforced by dependency lint.

## Rationale

Driver-specific types carry backend assumptions. Keeping them out of the graph, runtime, and control layers keeps those layers backend-neutral, so the persistence interfaces remain the only seam and a second backend serves the same interfaces.

## Alternatives

- Allowing driver imports anywhere behind a convention — rejected: driver types creep into backend-neutral layers, and the interface seam erodes until a second backend is unimplementable.
