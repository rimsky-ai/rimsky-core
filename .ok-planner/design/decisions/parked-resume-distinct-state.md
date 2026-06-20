---
decision: parked-resume-distinct-state
status: as-is
aliases: []
---

# Distinct `resuming` state for deadline-driven parked-resume

## Choice

The node-state machine carries a distinct `resuming` state, entered only when the parked sweep wakes a node whose resume deadline has elapsed. The transition is `parked + deadline_resume → resuming`, then `resuming + dispatch_claimed → running` when the dispatcher picks it up. The dispatcher branches on the run row's pre-claim state: a node-run claimed from `resuming` reuses its persisted dispatch-time attribute bag verbatim; a node-run claimed from `stale` rebuilds substitution from the current upstream attribute values.

The cascade-driven park-wake paths — a downstream cascade transition matching a parked subscriber, or an in-graph message-delivery matching a parked subscriber — continue to use the prior transition `parked + handler_resume → stale`. Those paths are not part of this decision's scope; they are tracked as a follow-up.

## Rationale

A parked node-run is a mid-resolve state, not a fresh dispatch. The node's view of its upstreams must stay fixed for the lifetime of the node-run: the substituted bag the executor saw when it parked must be the bag the executor sees when it resumes, regardless of what upstream nodes did during the park. Without this guarantee, a deadline-driven resume can silently rebuild the substitution context from current upstream attributes — an asynchronous ad-hoc invalidation of a node's inputs that breaks the contract every executor relies on.

Distinguishing the resume via a new state instead of reusing `stale` keeps the branch explicit at the persistence layer (separate value, separate index entry, separate query predicate) and at the dispatch layer (one IF on the acquisition's pre-claim state). The alternative — keep `stale`, distinguish at dispatch by reading the prior transition reason — entangles the bag-reuse decision with state-machine bookkeeping and is the shape that produced the original bug.

## Alternatives

Reuse `stale` with a transition-reason branch — rejected because the bag-reuse decision is then carried by a piece of bookkeeping state that gets clobbered by any intervening write, and because confusing a fresh-dispatch stale with a resume-from-park stale is precisely the bug this decision exists to prevent.

Persist the substitution snapshot to a sidecar table separate from the node-run's attributes — rejected because the node-run's persisted attribute bag is already the snapshot; introducing a second table for the same content is redundant. The bag is initialized at dispatch by the pre-dispatch substitution upsert and mutated only by the executor's writeback; it already carries the load-bearing snapshot. The fix is to read it on resume instead of rebuilding it.

Apply the snapshot-preserves invariant uniformly to all park-wake paths (cascade-driven included) — out of scope for this decision. The cascade-driven park-wake paths intentionally rebuild substitution today because the cascade is delivering new sender information the parked receiver subscribed to; preserving the snapshot there would require a deferred-cascade mechanism that processes the signal against a fresh node-run after the parked one settles. That is a separate architectural concern.
