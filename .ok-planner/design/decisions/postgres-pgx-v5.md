---
decision: postgres-pgx-v5
---

# Postgres driver is pgx v5

## Choice

Postgres access uses `jackc/pgx/v5` through its native interface, pinned to the v5 major line.

## Rationale

A native protocol-aware driver exposes what the persistence layer relies on — structured Postgres error detail, native type mapping, connection pooling — where a generic driver behind the standard SQL abstraction flattens it; v5 is the actively maintained major line.

## Alternatives

- `lib/pq`, the well-known incumbent — rejected: maintenance-mode, standard-abstraction-only, no native protocol surface.
- pgx through the standard SQL adapter — rejected: keeps the driver but hides the native interface behind the generic abstraction.
