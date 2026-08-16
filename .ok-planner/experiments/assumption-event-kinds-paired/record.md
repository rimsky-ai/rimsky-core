---
experiment: assumption-event-kinds-paired
commit: PENDING
---

# Looking for a release counterpart to every acquisition kind

## What it ran against

A `rimsky-all-in-one` stack with a `rimsky-claim-producer-filesystem` and one
named lock. Two instances run to completion: a fan-out that acquires a claim
and splits it into a subclaim, and a node that takes the named lock and a claim
of its own. The run reads the kinds each timeline carries, then asks the API
whether the missing counterparts are kinds at all.

## What was observed

The lock pair the assumption reasons from is real: the claim-and-lock timeline
carried both `lock_acquired` and `lock_released`.

The claim side acquires under one name and ends under another. The fan-out
emitted `subclaim.acquired` and no subclaim release kind of any spelling; the
claim ended as `claim_resolution.commit`.

The missing counterparts do not exist as kinds. `claim_released`,
`subclaim.released`, `subclaim.resolved`, `subclaim.resolution.commit` and
`claim_resolution.release` were each rejected with HTTP 400, while
`lock_released` was accepted.

The two claim acquisition kinds that do exist stayed empty. `claim_acquired`
and `claim_held` are both accepted by the filter and each returned zero rows
across a run in which a claim was acquired, held and committed —
`claim_resolution.commit` returned rows over the same window.
