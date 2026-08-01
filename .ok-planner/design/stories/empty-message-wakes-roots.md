---
story: empty-message-wakes-roots
status: as-is
---

# Operator wakes every structural root with an empty message

## Story

As an operator (or publisher) of a live instance, I can send an empty message to wake every structural root of the template without crafting a typed envelope, so that "start the default work" is a first-class one-call operation that uses the same path every other message does.

Operator-driven (or publisher-driven) whole-instance wake via the universal message-send surface: one empty-bodied message stale-marks every structural root of the template and the supervisor begins dispatching them, with idempotency-key replay protection identical to every other typed-message path.

Operators get a one-call "start the default work" verb without inventing a new endpoint, without crafting a typed envelope, and without per-template ceremony. The empty-message path is uniform with every other message path — same receipt route, same ledger, same idempotency surface, same delivery semantics — so operators do not learn a second mechanism for the common-case start.
