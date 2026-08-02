---
audit: message-sender-kind-discriminator
artifact: decision:message-sender-kind-discriminator
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:32Z
---

# The envelope sender-kind enum and the dedup sender-kind enum are distinct three-value enums

Supported. `rimsky_messages.sender_kind` carries `CHECK (sender_kind IN ('operator','publisher','instance'))` (`lib/foundation/persistence/postgres/migrations/001-initial.sql`), matching the envelope-side enum exactly, while `dedupSenderKind` in `lib/control/controlapi/messages.go` produces the separate `operator` / `publisher` / `anonymous` value set for the dedup ledger — `instance` never appears there (cascade-sends are blocked from reaching the operator/publisher wire path) and `anonymous` has no envelope-side meaning. `TestDedupSenderKind_AnonymousBucketDistinctFromOperatorAndPublisher` exercises the anonymous-bucket behavior the decision calls out, and `sendCascadeMessageInTx` in `lib/runtime/runner_send_message.go` shows the one path that mints an `instance`-kind envelope, keeping the two enums observably separate in code.
