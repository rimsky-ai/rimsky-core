---
decision: parked-resume-distinct-state
status: retired
---

# Distinct `resuming` state for deadline-driven parked-resume

## Retirement note

Superseded by the cascade no-re-invalidation redesign. The redesign generalizes the snapshot-preserves invariant across every in-flight state (pending, stale, running, held, parked) and across every resume path (deadline-wake, cascade-driven re-dispatch, operator-invalidate). Under the seven-state model, every dispatch loads its persisted attribute bag from its own row via `loadBagByRunID` — there is no special "rebuild substitution at dispatch" branch and therefore no need for a distinct `resuming` state to gate the bag-reuse decision. The deadline-driven parked-resume case collapses into `parked → stale` (the standard re-eligibility transition) followed by the standard dispatcher claim; bag preservation falls out of the general rule.

The new design that supersedes this decision: `cascade-no-reinvalidation-sketch` (2026-06-19). See `decision:walker-rule-per-sender-node` and `concept:node-run` for the load-bearing pieces of the replacement.

## Original choice (retained for history)

The node-state machine carries a distinct `resuming` state, entered only when the parked sweep wakes a node whose resume deadline has elapsed. The transition is `parked + deadline_resume → resuming`, then `resuming + dispatch_claimed → running` when the dispatcher picks it up. The dispatcher branches on the run row's pre-claim state: a node-run claimed from `resuming` reuses its persisted dispatch-time attribute bag verbatim; a node-run claimed from `stale` rebuilds substitution from the current upstream attribute values.

The cascade-driven park-wake paths — a downstream cascade transition matching a parked subscriber, or an in-graph message-delivery matching a parked subscriber — continue to use the prior transition `parked + handler_resume → stale`. Those paths are not part of this decision's scope; they are tracked as a follow-up.

## Original rationale (retained for history)

A parked node-run is a mid-resolve state, not a fresh dispatch. The node's view of its upstreams must stay fixed for the lifetime of the node-run: the substituted bag the executor saw when it parked must be the bag the executor sees when it resumes, regardless of what upstream nodes did during the park. Without this guarantee, a deadline-driven resume can silently rebuild the substitution context from current upstream attributes — an asynchronous ad-hoc invalidation of a node's inputs that breaks the contract every executor relies on.

Distinguishing the resume via a new state instead of reusing `stale` keeps the branch explicit at the persistence layer (separate value, separate index entry, separate query predicate) and at the dispatch layer (one IF on the acquisition's pre-claim state). The alternative — keep `stale`, distinguish at dispatch by reading the prior transition reason — entangles the bag-reuse decision with state-machine bookkeeping and is the shape that produced the original bug.

## Why the supersede works

The redesign's "every dispatch loads its persisted bag" rule eliminates the rebuild-on-dispatch branch entirely. With no rebuild branch to gate, there is nothing for a distinct `resuming` state to distinguish — the deadline-wake's parked → stale transition and a fresh cascade-driven pending → stale transition both produce a stale row whose persisted bag is what the dispatcher loads. The original decision's concern (entangling bag-reuse with bookkeeping state) is moot when there is no bag-reuse branch at all.
