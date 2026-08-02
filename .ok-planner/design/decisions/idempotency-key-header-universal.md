---
decision: idempotency-key-header-universal
---

# Idempotency on message send

## Choice

A mandatory idempotency-key header on the universal message-send endpoint (see `concept:message`, `concept:control-api`).

## Rationale

Replay-safe by construction.

## Alternatives

- An optional idempotency key — rejected: replay safety becomes caller-dependent, and the caller that omits it is the one that retries.
- Deduplicate by content hash — rejected: two legitimate identical sends are indistinguishable from a replay.
