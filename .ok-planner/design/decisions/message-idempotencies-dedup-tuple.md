---
decision: message-idempotencies-dedup-tuple
status: as-is
---

# Message dedup discriminator

## Choice

Per-instance, per-sender-kind, per-sender, per-sender-subject, per-idempotency-key (see `concept:message`).

## Rationale

Prevent cross-tenant + cross-kind replay collisions.
