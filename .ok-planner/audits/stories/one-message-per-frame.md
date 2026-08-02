---
audit: one-message-per-frame
artifact: story:one-message-per-frame
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:32Z
---

# Substitution from the message body is always well-defined because a frame never carries more than one message

Supported. `rimsky_frames.triggering_message_id` is `NOT NULL` and single-valued (schema comment in `lib/foundation/persistence/postgres/migrations/001-initial.sql`); `PickPendingMessagesForIdleInstances` selects exactly one oldest-pending message per idle instance (`ROW_NUMBER() ... WHERE rn = 1`, gated on no open frame existing for the instance), and `DeliverTriggeringMessage` in `lib/runtime/message_delivery.go` delivers only that frame's named trigger, never a batch — confirmed by `TestDeliverTriggeringMessage_OnlyDeliversFrameTrigger` and `TestRunTick_OpenNewFrames_PicksOldestPendingMessage`. Because each frame's message-receiver-node run's attribute bag is populated from exactly one message body before any substitution runs, a template's substitution directive against the message body always resolves against a single value, and the multi-message-bundling alternative that would force templates to handle ambiguity was rejected per `decision:one-message-per-frame`.
