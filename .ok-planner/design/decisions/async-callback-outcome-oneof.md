---
decision: async-callback-outcome-oneof
status: as-is
---

# Async-callback body shape

## Choice

Oneof `success` | `error` | `park` + optional `events` array; exactly one outcome key.

## Rationale

Type-safe state machine; explicit error handling.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
