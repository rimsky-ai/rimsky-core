---
trap: event-kinds-paired
release: d977250c
---
# Evidence set — acquisition kinds have matching release kinds throughout, so `subclaim.acquired` has a release counterpart and `claim_acquired` pairs with `claim_released` the way `lock_acquired` pairs with `lock_released`.

Source of the prior: sibling-symmetry — `lock_acquired`/`lock_released` present, `subclaim.acquired` and `claim_acquired` with no same-named release kind

## What the audit ran and observed (assumption record)

The experiment `assumption-event-kinds-paired` ran a fan-out that acquired a
claim and split it into a subclaim, plus a node that took a named lock and a
claim, both to completion. The lock pair holds: `lock_acquired` and
`lock_released` both appeared. Nothing else pairs. The fan-out emitted
`subclaim.acquired` with no release counterpart in the timeline, and none
exists to look for — `subclaim.released`, `subclaim.resolved` and
`subclaim.resolution.commit` were each rejected as unknown kinds with HTTP 400,
as were `claim_released` and `claim_resolution.release`, while `lock_released`
was accepted. The claim side does not end under a matching name at all: it ends
as `claim_resolution.commit`. And the two claim acquisition kinds that exist
give the operator nothing to reconcile — `claim_acquired` and `claim_held` are
both valid filter values and both returned zero rows across a run in which a
claim was acquired, held and committed, over the same window in which
`claim_resolution.commit` returned rows.

## Experiment record (experiment:assumption-event-kinds-paired)

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

Runnables: `src:.ok-planner/experiments/assumption-event-kinds-paired/` at the stamped commit.
