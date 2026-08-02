---
audit: persistence-dual-backend
artifact: decision:persistence-dual-backend
determination: supported
commit: b767a27d
audited: 2026-08-02T09:39:53Z
---

# Both Postgres and SQLite are supported, selected by a driver field in the unified config

Supported. The unified `rimsky.yml` schema (`lib/control/config`) carries a top-level `persistence.driver` string field alongside mutually-exclusive `postgres:`/`sqlite:` blocks; `persistence.Config.Validate` (`lib/foundation/persistence/open.go`) enforces exactly one of the two blocks is present for the chosen driver and rejects the other combinations, and `persistence.Open` wires to the matching backend package (`lib/foundation/persistence/postgres`, `lib/foundation/persistence/sqlite`), each self-registering via an `init()` call. Both backend packages exist, both implement the full `persistence.Database` interface, and both ship their own numbered migration sets.
