---
decision: upstream-gating-at-eligibility
status: as-is
---

# The all-upstreams guarantee is enforced at dispatch eligibility

## Choice

The dispatch-eligibility predicate carries the propagation-path-independent condition: a stale run is not eligible while any subscribed upstream has an in-flight run in the same frame. The wait-set ledger and its drained-rows substitution role are retained unchanged; self-edge ("drain my own queue") idioms and cycle handling keep working, pinned by scenario coverage of the deterministic diamond topology (see `concept:wait-set`, `concept:cascade`, `story:all-upstream-gating`).

## Rationale

A predicate cannot be forgotten by a new propagation path — the same chokepoint logic as the claimant guard; walk-side seeding would have to be remembered by every current and future stale-transition site.

## Alternatives

Uniform pessimistic seeding on every walk (rejected: per-path bookkeeping discipline — the duplicated-path disease again).
