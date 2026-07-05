---
story: empty-message-wakes-roots
status: as-is
---

# Operator wakes every structural root with an empty message

## Role

As an operator (or publisher) of a live instance, I can send an empty message to wake every structural root of the template without crafting a typed envelope, so that "start the default work" is a first-class one-call operation that uses the same path every other message does.

## Capability

Operator-driven (or publisher-driven) whole-instance wake via the universal message-emit surface: one empty-bodied message stale-marks every structural root of the template and the supervisor begins dispatching them, with idempotency-key replay protection identical to every other typed-message path.

## Business value

Operators get a one-call "start the default work" verb without inventing a new endpoint, without crafting a typed envelope, and without per-template ceremony. The empty-message path is uniform with every other message path — same receipt route, same ledger, same idempotency surface, same delivery semantics — so operators do not learn a second mechanism for the common-case start.

## Acceptance

I `POST /instances/{id}/messages` with an empty body (`{}`, or with `type: ""` explicit) and an `Idempotency-Key` against a live (unpaused) instance. A frame opens with `triggering_message_id` pointing at the empty-message envelope; every structural root — every node in the template whose author-declared `subscribes:` block is empty or absent — stale-marks in that frame and becomes dispatch-eligible; the frame proceeds through dispatch and settles as any other message-triggered frame does. A replay of the same emit with the same `Idempotency-Key` returns the original `message_id` with `200 OK` and opens no second frame. N empty messages with distinct keys produce N frames, each waking the roots.

## Falsifier

The empty-message emit lands in the ledger but no frame opens; OR the frame opens but no structural root stale-marks (no node-runs created); OR a non-root node with author-declared subscriptions (a `subscribes:` entry naming a specific upstream node-type) also stale-marks (the trigger overreaches); OR `Idempotency-Key` replay opens a second frame.

## Proof

Executable proof — emit empty message; observe one new frame with `triggering_message_id` matching the emit; observe stale-mark and dispatch on each structural root; observe non-root direct subscribers untouched; replay with the same key observes the original message id and no second frame.
