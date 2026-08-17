---
issue: sequenced-rounds-dispatch-out-of-order
kind: audit
category: conflicting
artifacts:
  - story:sequenced-preserves-cascade-rounds
  - concept:cascade-mode
status: verified
opened: 2026-08-16T08:55:50Z
---

# Sequenced cascade mode dispatches burst rounds out of arrival order

Sequenced cascade mode keeps its no-coalescing promise. It breaks its ordering promise. A node in sequenced cascade mode promises that when its upstream fires several rounds while the node is busy, every round dispatches, none coalesced, and in arrival order. The story names three uses: an audit trail, an accumulator, and rapid-flip detection. All three read that sequence. Rimsky gets the order wrong when the sender bursts back to back inside one frame. The newest round's receiver row becomes eligible first, because the gate evaluator runs per drained sender run and the earliest rounds still block on the upstream-in-flight probe. That newest round wins the race to dispatch, and the deferred sweep then drains the tail in sequence. The result is deterministic: 4,1,2,3; 3,1,2; 5,1,2,3,4. When the receiver waits at a breakpoint the rounds come out 1,2,3, so the guarantee holds only when nothing races. The ruling decides whether rimsky enforces the order or drops the promise.

## Options

- Make the pending-to-stale transition preserve order, so only the oldest still-queued round of a sender advances; cost: a gate-evaluator change that affects latency beyond this mode.
- Drop "in arrival order" and promise only that rimsky coalesces no round; cost: gives up the property the story's named uses depend on.

The ruling decides whether sequenced means ordered.

## Ruling

> Recommended ruling (/verify-issues): Enforce the order. In sequenced mode a round advances only when no older round of the same sender is still queued. Keep the story as written.
>
> Rationale: an accumulator or audit trail that reads rounds out of order is worse than one that coalesces, and the mode exists for the sequence. The cost lands only on receivers that opted into sequenced. Flip case: if the ordering constraint measurably stalls high-rate senders, weaken the promise to "in sequence order once the receiver is free" and say so.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
