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

A node in sequenced cascade mode promises that if its upstream fires several rounds while it is busy, every round dispatches — none coalesced — and in arrival order; the story's uses (an audit trail, an accumulator, rapid-flip detection) all read that sequence. Nothing is coalesced, but the order is wrong when the sender bursts back to back inside one frame: the newest round's receiver row becomes eligible first (gate evaluation is driven per drained sender run, and the earliest rounds are still blocked on the upstream-in-flight probe), so it wins the race to dispatch and the deferred sweep then drains the tail in sequence — 4,1,2,3; 3,1,2; 5,1,2,3,4, deterministically. When the receiver is held at a breakpoint the rounds come out 1,2,3, so the guarantee holds only when nothing races. The ruling decides whether the order is enforced or the promise dropped.

## Options

- Make the pending-to-stale transition order-preserving — only the oldest still-queued round of a sender may advance; cost: a gate-evaluator change with latency implications beyond this mode.
- Drop "in arrival order" and promise only that nothing is coalesced; cost: concedes exactly the property the story's named uses depend on.

The ruling decides whether sequenced means ordered.

## Ruling

> Recommended ruling (/verify-issues): Enforce the order — in sequenced mode, a round advances only when no older round of the same sender is still queued — and keep the story as written.
>
> Rationale: an accumulator or audit trail that sees rounds out of order is worse than one that coalesces, and the mode's whole reason for existing is the sequence; the cost lands only on receivers that opted into sequenced. Flip case: if the ordering constraint measurably stalls high-rate senders, weaken the promise to "in sequence order once the receiver is free" and say so.

<!-- Owner: this is a recommendation, not your decision. Leave it as-is to accept — the next /plan-sprint carries it, naming the generated/recommended batches at sign-off. Edit the text to redirect, empty the section to discuss live, or delete this note to adopt the ruling as your own. -->
