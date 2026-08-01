---
decision: walker-rule-per-sender-node
status: as-is
aliases: []
---

# Cascade walker accumulates by sender-node identity, not by pending-existence

## Choice

The cascade walker's accumulate-or-queue gate keys on sender-node identity: a cascade into a receiver accumulates into the receiver's latest cascade-driven pending unless the sender's node already holds a wait-set row there — a repeated sender-node seals that pending and opens a new one, which becomes the accumulation target. Multiple cascade-driven pendings can coexist per (receiver, run-scope); each transitions independently when its own wait-set drains and its gates clear, and the dispatcher's serialization gate orders their dispatch (see `concept:cascade` for the full gate).

## Rationale

Sender-node identity is the cascade-round boundary the opt-in receive modes rely on. A same-upstream re-cascade must materialize as a fresh round: under an accumulate-whenever-a-pending-exists rule it would collapse into the existing pending, the executor would never see the second round as a distinct event, `sequenced` mode would silently drop rounds, and the idempotent modes would have no round to compare against. Keying on the sender's node also handles the diamond and extra-root cases correctly — distinct sender-nodes cascading in the same phase accumulate into the receiver's single pending — so the new-pending branch fires exactly when a sender-node repeats, which is exactly the round boundary.

## Alternatives

- Pending-existence accumulation (accumulate iff any pending exists, regardless of which sender-nodes contributed) — rejected: it collapses cascade rounds and breaks the opt-in modes (sequenced, idempotent-*). The default most-recent mode would survive, since its "delete prior" applies regardless of how the pending was built, but the per-round granularity the other modes need would be lost, and recovering it later in mode logic would require per-round provenance the wait-set doesn't naturally carry.
- Multiple-pendings-forbidden (always accumulate, never create a second pending) — rejected: it forces same-upstream re-cascades into the existing pending, losing the round boundary even more aggressively than pending-existence accumulation.
- One-pending-per-cascade-event (every cascade event creates a new pending, no accumulation) — rejected: the dispatcher then sees O(cascade-events) stales per (receiver, run-scope), which is unbounded and defeats the point of the wait-set entirely — the receiver would dispatch once per cascade event regardless of mode.
