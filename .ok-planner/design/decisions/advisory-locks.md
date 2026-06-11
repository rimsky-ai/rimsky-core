---
decision: advisory-locks
status: as-is
---

# Named cross-process coordination

## Choice

Postgres advisory locks + sqlite equivalent at session level.

## Rationale

Migration ownership, scheduler-tick ownership, per-scope serialization.
