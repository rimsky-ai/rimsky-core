---
decision: message-sender-kind-discriminator
status: as-is
---

# Message envelope sender

## Choice

The `rimsky_messages.sender_kind` column is the three-value enum `operator` / `publisher` / `instance`.

## Rationale

Namespace sender strings by source on the persisted envelope. The orthogonal `sender_subject` column carries the actor identity (api-key id, publisher subscription, or the `anonymous` sentinel for anonymous-mode operator emits). The idempotency-dedup tuple in `rimsky_message_idempotencies` has its own three-value `sender_kind` column that differs from the envelope's by one value — `operator` / `publisher` / `anonymous` (no `instance`, since instance-sender messages are blocked at the wire by the operator-or-publisher gate), where `anonymous` buckets anonymous-mode operator emits separately so the bootstrap admin's later emits don't dedup against the anonymous-floor emits that preceded the key mint. The two `sender_kind` columns are not the same enum and should not be conflated.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
