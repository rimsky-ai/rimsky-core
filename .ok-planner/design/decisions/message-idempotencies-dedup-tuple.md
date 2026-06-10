---
decision: message-idempotencies-dedup-tuple
status: as-is
---

# Message dedup discriminator

## Choice

`(instance_id, sender_kind, sender, sender_subject, idempotency_key)`.

## Rationale

Prevent cross-tenant + cross-kind replay collisions.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
