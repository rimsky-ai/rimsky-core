---
audit: message-bus
artifact: story:message-bus
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:32Z
---

# Sender sends idempotent messages into an instance's bus with dedup, history, and get-by-ID

Supported. `handleCreateMessage` in `lib/control/controlapi/messages.go` requires an `Idempotency-Key` header on every send, computes a dedup tuple, and on a replayed key returns the original `message_id` with HTTP 200 instead of 201 and without a second ledger insert — verified by `TestCreateMessage_IdempotencyKeyDuplicateReturnsExisting` and `TestMCPMessageSend_CallerSuppliedIdempotencyKeyReplaysInsteadOfDoubleSending`. The three send sites named by the underlying `concept:message` — operator API, publisher API, and cascade-send (`sendCascadeMessageInTx` in `lib/runtime/runner_send_message.go`) — all route through the same `EnqueueMessage` chokepoint and the same `rimsky_message_idempotencies` ledger. List-by-instance and get-by-ID are exercised end-to-end by `TestMessages_PostListGet`, which posts a message, lists it back on the instance's history endpoint, and fetches it individually by ID, checking sender, sender_kind, and type on both surfaces.
