---
tension: event-vocabulary-implies-delivery
category: vocabulary-drift
status: resolved
affects:
  - signal
  - node-subscription
resolution:
  shape: keep-names-with-durable-accuracy-text
  doc-sweep:
    - concepts/signal.md (payload consumed at walk-time, NOT propagated to subscribers; subscribers receive the wake)
    - concepts/node-subscription.md (invalidate-then-pull accuracy text)
  summary: |
    Chose the second resolution candidate: keep the reactive names and
    make the invalidate-then-pull accuracy text durable at every
    reactive primitive. concept:signal states plainly that the signal
    payload is consumed at walk-time (CEL gating, audit) and is not
    propagated to subscribers — subscribers receive the wake, not the
    payload — and concept:node-subscription carries the same model.
    The vocabulary itself was tightened separately: the named-event
    mechanism retired in favor of signal, and the send-vs-emit ruling
    (messages are SENT, signals are EMITTED) swept repo-wide. The
    rename candidate (subscribe → watch, payload → body) is rejected:
    "subscribe" is standard vocabulary for reactive-recompute systems,
    and the misdesign risk the tension guarded against is covered by
    the positive statements at the primitives.
---

# The reactive vocabulary ("emit", "subscribe", "payload") models a delivery system, but the engine is invalidate-then-pull

## What is muddy

Before this resolution: the reactive primitives are named in pub-sub terms — a node **emits** a **signal** that **carries a payload**, and other nodes **subscribe** to it. That vocabulary describes a delivery system: messages flowing from a producer to a consumer, one consumer dispatch per emission, the payload riding along the edge.

The engine does none of that. It is **invalidate-then-pull / reactive-recompute**: an emitted signal marks the subscribing receiver stale via the wait-set, the receiver is rescheduled, and on its next run it pulls the *latest* persisted value via substitution. Nothing rides the edge — the signal payload is consumed at walk-time (CEL `when:` gating) and is not propagated to subscribers (per `concept:signal`). Multiple emissions in a frame collapse to a single receiver dispatch, and the receiver always reads the most-recent value — never the per-emission stream.

The gap between the words and the mechanism is not cosmetic. It misled a design once: an agent proposed a per-emission payload-binding feature — binding "the triggering emission's payload" into the dispatched receiver — which cannot exist, because there is no per-emission dispatch and no triggering emission to bind. The feature was dropped once the mismatch surfaced.

## Why it matters

Vocabulary that implies the wrong execution model produces wrong designs, wrong operator mental models, and wrong expectations about cardinality (one-dispatch-per-emission vs. one-per-frame-latest) and about what data is available to a dispatched node (the triggering payload vs. the latest persisted value).

## Evidence

- A dropped per-emission payload-binding design, abandoned once the invalidate-then-pull mechanism was understood. The corrected accuracy text lives in `concept:signal` and `concept:node-subscription`.
