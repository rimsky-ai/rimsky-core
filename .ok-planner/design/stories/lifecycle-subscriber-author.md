---
story: lifecycle-subscriber-author
status: as-is
---

# Service author writes lifecycle subscriber

## Role

As a service author writing a lifecycle subscriber, I can implement the `concept:lifecycle-subscriber` protocol — seven callbacks covering template registered, deployed, undeployed, deregistered, instance created and terminated, and run-scope terminal — and register it as an active subscriber, with rimsky firing each callback synchronously at the corresponding lifecycle transition carrying the relevant context (template hash, instance ID, run-scope ID, service bindings, owner key, terminal reason), so that I react to rimsky lifecycle events from an external service.

## Capability

Public lifecycle-subscriber protocol surface (seven synchronous callbacks, see `concept:lifecycle-subscriber`). Rimsky fires each callback at the corresponding transition with documented context fields; the subscriber's response is honored at the close site.

## Business value

External services react to rimsky lifecycle events synchronously — a subscriber that needs to react to template-deployed or run-scope-terminal can wire in and rimsky honors the subscriber's response.

