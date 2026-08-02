---
story: lifecycle-subscriber-author
---

# Service author writes lifecycle subscriber

## Story

As a service author writing a lifecycle subscriber, I can implement the `concept:lifecycle-subscriber` protocol — seven callbacks covering template registered, deployed, undeployed, deregistered, instance created and terminated, and run-scope terminal — and register it as an active subscriber, with rimsky firing each callback synchronously at the corresponding lifecycle transition carrying the relevant context (template hash, instance ID, run-scope ID, service bindings, owner key, terminal reason), so that I react to rimsky lifecycle events from an external service.
