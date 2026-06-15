---
story: all-upstream-gating
status: as-is
---

# Template author relies on all-upstream gating for fan-in shapes

## Role

As a template author building a fan-in shape (a node subscribing to several upstream siblings), I can rely on the receiver dispatching only after all of its in-flight upstreams in the frame have resolved — regardless of how their staleness arrived — so that the receiver never runs against a half-settled upstream set.

## Capability

The dispatch-eligibility predicate carries a propagation-path-independent condition: a stale run is not eligible while any subscribed upstream has an in-flight run in the same frame, whether the staleness arrived by invalidation walk or by sender settlement. The wait-set ledger's drained-rows role continues to feed substitution; self-edge and cycle idioms are first-class under the present-state predicate (see `concept:wait-set`, `concept:cascade`, `decision:upstream-gating-at-eligibility`).

## Business value

Fan-in topologies (diamonds, N-parent receivers) compute from the full upstream set, not whichever subset happened to settle first — and the guarantee cannot be forgotten by a new staleness-propagation path, because it lives in the eligibility predicate rather than in per-path bookkeeping.

## Acceptance

In a diamond or N-parent shape where the upstream staleness propagates by sender settlement (not only by an invalidation walk), the receiver runs exactly once per frame, after the last in-flight upstream resolves, and its substitution context contains all upstream contributions.

## Falsifier

A receiver observed dispatching while a subscribed upstream still has an in-flight run in the same frame; or a receiver that runs early and is never re-fired when stragglers settle, leaving the frame's result computed from a partial upstream set.

## Proof

Executable proof — a deterministic scenario test builds the diamond with settlement-propagated staleness, holds one upstream open via an injection hook, and asserts the receiver is not dispatch-eligible until the held upstream resolves — then asserts single dispatch with the full upstream set in the substitution context.
