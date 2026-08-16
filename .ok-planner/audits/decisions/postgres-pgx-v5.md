---
audit: postgres-pgx-v5
artifact: decision:postgres-pgx-v5
text: compliant
implementation: unsupported
commit: PENDING
audited: 2026-08-16T04:47:25Z
checked: 19
unaccounted: 4
---

# Whether all Postgres access uses the pinned v5 driver through its native interface

Unsupported at four of nineteen sites, though the driver identity and the pin are solid. Three module manifests require the driver at one identical v5 version, a manifest fitness test fails if the pin disappears, and the rejected incumbent driver appears nowhere. Fifteen of the 19 packages that reach Postgres use the native interface as the choice describes — the core persistence and pooling packages, the Postgres claim producer's server and store, the lineage subscriber, the CLI, and the test-support and scenario packages all work in native pooled connections, native rows, native transaction options, the structured Postgres error type, and large objects. The four sensor state stores do not: each registers the driver's standard-abstraction adapter and opens a generic database handle, which is exactly the alternative the decision records as rejected for hiding the native surface. Nothing mechanical guards this — the dependency lint governs where the driver may be imported, not how it is used.

## Unaccounted

- The cron sensor's state store opens Postgres through the standard SQL abstraction via the driver's adapter.
- The HTTP sensor's state store does the same.
- The object-store sensor's state store does the same.
- The webhook sensor's state store does the same.
