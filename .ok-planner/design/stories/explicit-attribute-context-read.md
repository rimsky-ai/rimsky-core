---
story: explicit-attribute-context-read
status: as-is
---

# Template author reads an upstream attribute without firing the receiver

## Role

As a template author, I can read an upstream node's attribute via substitution while declaring that the read does not fire my receiver on the sender's change, so my receiver's wake-up is governed only by its other explicit subscriptions.

## Capability

The receiver carries an explicit `subscribes:` entry naming the upstream's signal type (or a wildcard) with `wake_on_change: false`. A matching emission from that upstream inserts a wait-set row carrying the upstream's data into the receiver's substitution context — but does not stale-mark the receiver. The receiver dispatches only when one of its other subscriptions fires it; when it does, its substitution context contains the upstream's value if the upstream settled in the same frame AFTER the receiver was already pulled into the frame. When the upstream settles before the receiver enters the frame, the substitution ref resolves via the existing fallback / lenient / optional routing — authors needing deterministic carry-through regardless of intra-frame ordering use `force_upstream_refresh: true` instead.

## Business value

Template authors decouple read access from cascade dispatch. A receiver that needs an upstream's value at dispatch but should not run every time the upstream changes can express that contract directly on the subscription — no implicit auto-subscribe widens the cascade behind the author's back.

## Acceptance

An author writes a template where receiver A reads `{{nodes.X.attribute.Y}}` and carries a subscription entry naming X's `attribute/Y/changed` (or `attribute/*`) with `wake_on_change: false`. After deploy: when X settles `attribute/Y/changed` and A's other subscriptions don't match, A does not dispatch. When A is dispatched via its other subscriptions and X is in the same frame AND X settles after A has been pulled into the frame, A's substitution context includes X's value at dispatch. When X settles before A enters the frame, A's substitution ref for X resolves through the existing fallback / lenient / optional routing (the wait-set row is anchored to A's in-flight run and is not recorded retroactively).

## Falsifier

A's other subscriptions don't match, X emits `attribute/Y/changed`, A dispatches anyway — meaning the cascade is firing A on the suppressed edge.

## Proof

All-of-the-above — an example template exhibiting the gated subscription plus context-gathering reads, and an executable proof that walks the two scenarios (X changes alone → A does not fire; A's gate matches → A fires and reads X's value).
