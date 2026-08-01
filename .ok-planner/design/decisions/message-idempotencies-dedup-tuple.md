---
decision: message-idempotencies-dedup-tuple
status: as-is
---

# Message dedup discriminator

## Choice

The message-idempotency dedup key is the full tuple: instance, sender kind, sender, sender subject, idempotency key (see `concept:message`).

## Rationale

Scoping dedup to the full sender identity prevents cross-tenant and cross-kind replay collisions: one sender's idempotency key can never suppress another sender's message.

## Alternatives

- Dedup on instance + idempotency key alone — rejected: keys chosen independently by different senders (or by the same actor through different kinds) collide, letting one sender's replay silently swallow another's message.
