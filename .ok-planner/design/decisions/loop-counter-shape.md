---
decision: loop-counter-shape
---

# Loop-counter shape

## Choice

The loop-counter utility kind is a carry-forward counter: a required strictly-positive maximum-count input attribute, an executor-owned read-only count attribute carried forward across dispatches via the outcome's attributes delta, and two declared tags on the Success outcome — a step-iteration tag while the count is below the maximum, a done tag on the terminal step.

## Rationale

Minimum surface for "count up to N, emit on each step, include a different tag on the terminal step." Both tags are observable from downstream subscriptions filtering on the success terminal's tags; the count attribute is visible to other nodes (and to the loop-counter itself across dispatches via carry-forward). Scope-bounded carry-forward is intra-frame (per `decision:run-scope-is-per-frame`): the count carries across dispatches within one frame's RunScope (the standard cascade-self-edge case) and resets on every new frame, every sub-graph invocation, and every fan-out partition. Cross-frame counting (a sequence of operator-triggered or message-driven iterations) is not within this utility's surface — orchestrators that need it ferry the count through a message body via the standard message-borne cross-frame coupling path (see `concept:message`).

## Alternatives

- Runtime-owned iteration counting (the frame engine tracking rounds per node) — rejected: bakes loop semantics into the cascade walker for one utility's benefit; the counter as an ordinary executor keeps iteration observable and composable like any other node.
- A cross-frame-durable counter — rejected: state crossing a frame boundary through anything but a message envelope violates the structural frame-isolation invariant (`decision:frame-isolation-is-structural`).
