---
decision: idempotency-key-header-universal
status: as-is
---

# Idempotency on message emit

## Choice

A mandatory idempotency-key header on the universal message-emit endpoint (see `concept:message`, `concept:control-api`).

## Rationale

Replay-safe by construction.
