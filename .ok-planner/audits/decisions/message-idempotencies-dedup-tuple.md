---
audit: message-idempotencies-dedup-tuple
artifact: decision:message-idempotencies-dedup-tuple
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:32Z
---

# Dedup key is the full (instance, sender kind, sender, sender subject, idempotency key) tuple

Supported. `rimsky_message_idempotencies` (`lib/foundation/persistence/postgres/migrations/001-initial.sql`) has `PRIMARY KEY (instance_id, sender_kind, sender, sender_subject, idempotency_key)`, matching the decision's tuple exactly, and `MessageIdempotencyRow` / `InsertOrLookup` in `lib/foundation/persistence/message_idempotencies.go` carry all five fields into the lookup. `TestCreateMessage_IdempotencyKeyDistinctSendersDoNotCollide` and `TestDedupSenderKind_AnonymousBucketDistinctFromOperatorAndPublisher` exercise the cross-sender non-collision the tuple is chosen to guarantee.
