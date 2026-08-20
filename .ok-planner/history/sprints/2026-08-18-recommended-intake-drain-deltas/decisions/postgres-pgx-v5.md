---
decision: postgres-pgx-v5
---

# Postgres driver is pgx v5

## Choice

Postgres access uses `jackc/pgx/v5` through its native interface, pinned to the v5 major line. The rule is repo-wide and covers every package that reaches Postgres, the bundled services' own state stores included. The dependency lint denies the driver's standard-SQL adapter package everywhere, so the native interface is the only way in.

## Rationale

A native protocol-aware driver exposes what the persistence layer relies on — structured Postgres error detail, native type mapping, connection pooling — where a generic driver behind the standard SQL abstraction flattens it; v5 is the actively maintained major line.

The project keeps one idiom per job, and that matters more than the adapter's convenience at any one site. A second way to reach Postgres invites a contributor to copy the wrong one into the persistence layer. The layering lint does not catch that: it governs where the driver may be imported, not which of the driver's surfaces a caller picks. Denying the adapter package closes the gap.

## Alternatives

- `lib/pq`, the well-known incumbent — rejected: maintenance-mode, standard-abstraction-only, no native protocol surface.
- pgx through the standard SQL adapter — rejected: keeps the driver but hides the native interface behind the generic abstraction.
- Permit the adapter in a bundled service's own state store while requiring the native interface in the persistence layer — rejected: two Postgres idioms in one tree, with only code review to keep each on its own side.
