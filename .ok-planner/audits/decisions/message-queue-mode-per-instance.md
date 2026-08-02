---
audit: message-queue-mode-per-instance
artifact: decision:message-queue-mode-per-instance
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:32Z
---

# Coalesce mode is a single per-instance setting, uniform across message types, with a validator warning on multi-type templates

Supported. `message_queue_mode` is a single field on the template spec and the instance row (`lib/foundation/spec`, `lib/graph/node`), with no per-message-type variant anywhere in the schema, and `EnqueueMessage`'s coalesce branch in `lib/runtime/message_delivery.go` cancels all prior pending messages for the instance regardless of type. `validateMessageQueueMode` in `lib/graph/node/template_validator.go` emits a non-fatal `ValidationWarning` naming every declared type when a coalesce-mode template declares two or more distinct types, matching the decision's stated validator behavior; the naming distinction from the per-node `cascade_mode` setting holds — the two are separate fields on separate rows (`rimsky_nodes.cascade_mode` vs `rimsky_instances.message_queue_mode`) with disjoint value sets.
