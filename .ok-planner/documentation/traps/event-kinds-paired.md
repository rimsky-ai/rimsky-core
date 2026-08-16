---
trap: event-kinds-paired
release: d977250c
demonstration: experiment:assumption-event-kinds-paired
---
## Assumption

As operator reconciling a timeline, I would take it that acquisition kinds have matching release kinds throughout, so `subclaim.acquired` has a release counterpart and `claim_acquired` pairs with `claim_released` the way `lock_acquired` pairs with `lock_released`.

sibling-symmetry — `lock_acquired`/`lock_released` present, `subclaim.acquired` and `claim_acquired` with no same-named release kind

## Actual behavior

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
