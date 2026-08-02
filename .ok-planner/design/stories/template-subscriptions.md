---
story: template-subscriptions
---

# Template author wires CEL-predicated subscriptions

## Story

As a template author wiring upstream-event-driven nodes, I can declare a subscription entry with a canonical signal type-path (exact or trailing-wildcard prefix) plus an optional CEL predicate over the signal payload and have the runtime fire the node only when a matching signal arrives whose payload satisfies the predicate, so that I write reactive nodes that filter precisely on what triggers them.
