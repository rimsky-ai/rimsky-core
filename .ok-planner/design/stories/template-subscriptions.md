---
story: template-subscriptions
status: as-is
---

# Template author wires CEL-predicated subscriptions

## Role

As a template author wiring upstream-event-driven nodes, I can declare a `subscribes:` entry with a canonical signal type-path (exact or trailing-`*` prefix) plus an optional CEL predicate over the signal payload and have the runtime fire the node only when a matching signal arrives whose payload satisfies the predicate, so that I write reactive nodes that filter precisely on what triggers them.

## Capability

`subscribes:` declaration with type-path matching (exact or trailing-`*` prefix) plus an optional CEL predicate over the signal payload; the runtime fires the node only on matches.

## Business value

Template authors write reactive nodes that filter precisely on what triggers them — both by signal type and by payload content — without inline filtering inside the node.

## Acceptance

A template with a node declaring a subscription to a signal type-path with a CEL predicate (e.g. `payload.tenant == "alpha"`); when the runtime produces a signal of that type whose payload matches the predicate, the subscribed node fires; when payload doesn't match, the node doesn't fire. Trailing-`*` prefix matches every type-path with that prefix.

## Falsifier

Subscription fires the node on a non-matching payload (predicate ignored), OR doesn't fire on a matching one, OR trailing-`*` doesn't match its prefix.

## Proof

Executable proof.
