---
audit: idempotency-status-code-distinction
artifact: decision:idempotency-status-code-distinction
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:38:14Z
---

# 201-vs-200 status code distinguishes fresh sends from idempotent replays

Supported. `lib/control/controlapi/messages.go::handleCreateMessage` inserts an idempotency row (`MessageIdempotencies().InsertOrLookup`) and returns `http.StatusCreated` (201) when the insert is fresh, or `http.StatusOK` (200) with the original `message_id` when the key already existed (`replayed` branch) — no body-level replay marker is used. `TestCreateMessage_IdempotencyKeyDuplicateReturnsExisting` in `lib/control/controlapi/messages_test.go` asserts 201 on the first send, 200 on the replay, and that the replay's `message_id` equals the first send's, matching the decision's claim exactly.
