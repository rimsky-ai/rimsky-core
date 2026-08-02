---
audit: instance-create-is-idle
artifact: story:instance-create-is-idle
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:34Z
---

# Operator creates an idle instance

Supported. `TestStory_InstanceCreateIsIdle` posts `/v1/instances` for a template with a root node and a downstream subscriber, then polls for 1.5s asserting the instance's frame list and message ledger stay empty, every materialized node carries a null `frame_id` and zero runs in every state, and no `terminal/success` event exists for the root — only after that window does it confirm node rows exist (create-time side effect) and exactly one `OnInstanceCreated` lifecycle event fired. `handleCreateInstance` in the control API's instance-creation transaction corroborates this at the source: it materializes node rows and starts publisher subscriptions but never posts a message, opens a frame, or dispatches — invoking work is a distinct, separate operator action (posting a message) from creating the instance.
