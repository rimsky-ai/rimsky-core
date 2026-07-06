---
decision: idempotency-key-header-universal
status: as-is
---

# Idempotency on message send

## Choice

A mandatory idempotency-key header on the universal message-send endpoint (see `concept:message`, `concept:control-api`).

## Rationale

Replay-safe by construction.
