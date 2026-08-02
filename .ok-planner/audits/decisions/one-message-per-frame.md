---
audit: one-message-per-frame
artifact: decision:one-message-per-frame
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:32Z
---

# Every frame delivers at most one message; N pending messages produce N sequential frames

Supported. `pickPendingForIdleInstancesSQL` in `lib/foundation/persistence/postgres/messages.go` ranks pending messages per instance by receipt order and selects only `rn = 1`, gated on no open frame existing for that instance (`NOT EXISTS ... f.ended_at IS NULL`), so a new frame for the next pending message can only open once the prior one has ended — the mechanism that turns N pending messages into N sequential frames rather than one bundled frame. `runOpenNewFrames` in `lib/graph/frame/engine.go` and `DeliverTriggeringMessage` in `lib/runtime/message_delivery.go` both operate on the frame's single named `triggering_message_id`, never a set. `TestRunTick_OpenNewFrames_PicksOldestPendingMessage` and `TestDeliverTriggeringMessage_OnlyDeliversFrameTrigger` cover the oldest-first pick and the single-delivery guarantee; the rejected `coalesce` frame-bundling alternative the decision names has no code path anywhere in the frame-open logic.
