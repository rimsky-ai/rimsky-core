---
decision: persistence-dual-backend
status: as-is
---

# Backend support

## Choice

Both Postgres and SQLite, selected by `persistence.driver` config.

## Rationale

SQLite for dev/test, Postgres for prod.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
