---
decision: async-callback-outcome-oneof
status: as-is
---

# Async-callback body shape

## Choice

Oneof `success` | `error` | `park` + optional `events` array; exactly one outcome key.

## Rationale

Type-safe state machine; explicit error handling.
