---
story: template-subscriptions
status: as-is
---

# Template author wires CEL-predicated subscriptions

## Story

As a template author wiring upstream-event-driven nodes, I can declare a subscription entry with a canonical signal type-path (exact or trailing-wildcard prefix) plus an optional CEL predicate over the signal payload and have the runtime fire the node only when a matching signal arrives whose payload satisfies the predicate, so that I write reactive nodes that filter precisely on what triggers them.

Subscription declaration with type-path matching (exact or trailing-wildcard prefix) plus an optional CEL predicate over the signal payload; the runtime fires the node only on matches.

Template authors write reactive nodes that filter precisely on what triggers them — both by signal type and by payload content — without inline filtering inside the node.
