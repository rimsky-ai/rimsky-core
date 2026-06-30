---
decision: walker-rule-per-sender-node
status: as-is
aliases: []
---

# Cascade walker accumulates by sender-node identity, not by pending-existence

## Choice

The cascade walker uses a per-sender-node accumulation gate. When a cascade walk targets receiver R from sender S in the current frame:

1. Find R's latest (most-recently-created) cascade-driven pending run for the same (node, run-scope), if any.
2. If R has no pending → create a new pending and insert the wait-set row referencing S.
3. If R has a pending AND S's node IS NOT already in the pending's wait-set sender-nodes → accumulate (insert the wait-set row into the existing pending).
4. If R has a pending AND S's node IS already in the pending's wait-set sender-nodes → create a NEW pending (the previous pending is sealed; subsequent cascades from other sender-nodes accumulate into the new one).

Multiple cascade-driven pendings can coexist per (R, run-scope). The latest pending is always the accumulation target. Each pending transitions independently when its own wait-set drains and its gates clear; the dispatcher's serialization gate orders their dispatch.

## Rationale

The walker has to decide, on each cascade event, whether to **mutate an existing pending's wait-set** (accumulate) or **create a new pending** (queue a fresh round). Two candidate rules:

- **Rule (a) — per sender-node**: accumulate iff the sender's node is not already in the pending's wait-set.
- **Rule (b) — pending-existence**: accumulate iff any pending exists (regardless of which sender-nodes contributed).

Rule (a) is correct because it preserves the cascade-round semantic that `sequenced` and the idempotent modes rely on. Under rule (b), a same-upstream re-cascade during a single pending phase collapses into the existing pending and the executor never sees the second round as a distinct event; `sequenced` mode silently drops cascade rounds, and `idempotent-queue` / `idempotent-settled` cannot compare against a round that never materialized. Under rule (a), a same-upstream re-cascade triggers a new pending — exactly the "this is a fresh round, with its own input" semantic the modes need.

Rule (a) is also correct for the diamond and extra-root cases. When B and C both cascade to D in the same cascade phase, both wait-set rows accumulate into D's single pending (their sender-nodes are distinct). When A and X both cascade to B (A→B, X→B), both rows accumulate into B's single pending. The "new pending" branch fires only when a sender-node repeats — which is exactly the cascade-round boundary.

## Alternatives

Rule (b) — pending-existence — rejected because it collapses cascade rounds and breaks the opt-in modes (sequenced, idempotent-*). The default mode (most-recent) would still work under rule (b), since most-recent's "delete prior" applies regardless of how the pending was built, but the system would lose the per-round granularity the other modes need. Picking the right granularity at the walker layer is cheap; correcting it later in mode logic would require carrying per-round provenance that the wait-set doesn't naturally have.

Multiple-pendings-forbidden (always accumulate, never create a second pending) — rejected because it forces same-upstream re-cascades into the existing pending, losing the round boundary even more aggressively than rule (b).

One-pending-per-cascade-event (every cascade event creates a new pending, no accumulation) — rejected because the dispatcher then sees O(cascade-events) stales per (node, run-scope), which is unbounded and defeats the point of the wait-set entirely (each pending would have a single wait-set row, and the receiver would dispatch once per cascade event regardless of mode).
