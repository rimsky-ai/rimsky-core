---
decision: persistence-dual-backend
status: as-is
---

# Backend support

## Choice

Both Postgres and SQLite, selected by a driver-selector field in the unified config.

## Rationale

SQLite for dev/test, Postgres for prod.
