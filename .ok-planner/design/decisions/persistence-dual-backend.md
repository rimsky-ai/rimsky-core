---
decision: persistence-dual-backend
status: as-is
---

# Backend support

## Choice

Both Postgres and SQLite, selected by a driver-selector field in the unified config.

## Rationale

SQLite for dev/test, Postgres for prod.

## Alternatives

- Postgres only — rejected: zero-config local dev and the all-in-one image would require a running Postgres just to boot.
- SQLite only — rejected: no production-grade concurrent multi-process backend.
