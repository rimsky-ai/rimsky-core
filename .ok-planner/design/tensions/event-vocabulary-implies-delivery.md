---
tension: event-vocabulary-implies-delivery
category: vocabulary-drift
status: open
affects:
  - named-event
  - node-subscription
---

# The reactive vocabulary ("event", "subscribe", "payload", "push") models a delivery system, but the engine is invalidate-then-pull

## What is muddy

The reactive primitives are named in pub-sub terms — a node **emits** a named **event** that **carries a payload**, and other nodes **subscribe** to it. That vocabulary describes a delivery system: messages flowing from a producer to a consumer, one consumer dispatch per produced message, the payload riding along the edge.

The engine does none of that. It is **invalidate-then-pull / reactive-recompute**: an upstream transition marks the subscribing receiver stale, the receiver is rescheduled, and on its next run it pulls the *latest* persisted value via substitution. Nothing rides the edge. Multiple emissions in a frame collapse to a single receiver dispatch (the wait-set is keyed so N emissions become one), and the receiver always reads the most-recent value — never the per-emission stream.

The gap between the words and the mechanism is not cosmetic. It has already misled a design: an agent proposed a per-emission event-payload-binding feature — binding "the triggering emission's payload" into the dispatched receiver — which cannot exist, because there is no per-emission dispatch and no triggering emission to bind. The feature was dropped once the mismatch surfaced. The same trap will recur for the next reader who takes "subscribe to the event's payload" at face value.

## Why it matters

Vocabulary that implies the wrong execution model produces wrong designs, wrong operator mental models, and wrong expectations about cardinality (one-dispatch-per-emission vs. one-per-frame-latest) and about what data is available to a dispatched node (the triggering payload vs. the latest persisted value). Every reader pays the cost of discovering the mismatch independently.

## Resolution candidates (do NOT pick)

- Rename the reactive vocabulary toward invalidation and reactive-recompute terms so the names match the mechanism: rename "event" to "response", rename "subscribe" to "watch", rename "payload" to "body", and drop the redundant single-member "trigger" namespace wrapper from the substitution grammar. Decide the exact renaming and migration in a future design-refinement pass; this is a vocabulary change across the concept catalog and the template DSL, not a behavior change.
- Alternatively, keep the current names but add a prominent, durable disclaimer at every reactive primitive stating plainly that the model is invalidate-then-pull and that there is no per-emission delivery — accepting the standing risk that the disclaimer is missed.

## Evidence

- A dropped per-emission event-payload-binding design, abandoned once the invalidate-then-pull mechanism was understood. The corrected accuracy text now lives in `concept:named-event` and `concept:node-subscription`.
