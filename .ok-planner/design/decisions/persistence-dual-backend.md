---
decision: persistence-dual-backend
status: as-is
---

# Backend support

## Choice

Both Postgres and SQLite, selected by `persistence.driver` config.

## Rationale

SQLite for dev/test, Postgres for prod.
