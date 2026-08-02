---
audit: message-queue-coalesces-pending
artifact: story:message-queue-coalesces-pending
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:32Z
---

# Operators choose per-instance whether pending wake messages backlog or coalesce

Supported. `message_queue_mode` (`backlog` default, `coalesce`) is declared on the template, validated by `validateMessageQueueMode` in `lib/graph/node/template_validator.go`, materialized onto the instance row at creation, and overridable per instance at creation time (`lib/control/controlapi/instances.go`, `MessageQueueMode` request field overriding the template default). `EnqueueMessage` in `lib/runtime/message_delivery.go` cancels every prior pending message for the instance in the same transaction when the mode is `coalesce`, uniformly across message types, and leaves every prior pending message untouched under `backlog`; both behaviors are exercised end-to-end by `TestMessageQueueCoalesce_DropsPriorPendingOnReceipt` and `TestMessageQueueCoalesce_BacklogModePreservesEveryMessage` in `test/scenarios/message_queue_coalesce_test.go`.
