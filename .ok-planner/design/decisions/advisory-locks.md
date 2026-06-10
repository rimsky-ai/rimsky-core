---
decision: advisory-locks
status: as-is
---

# Named cross-process coordination

## Choice

Postgres advisory locks + sqlite equivalent at session level.

## Rationale

Migration ownership, scheduler-tick ownership, per-scope serialization.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
