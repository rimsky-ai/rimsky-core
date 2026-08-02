---
audit: upstream-gating-at-eligibility
artifact: decision:upstream-gating-at-eligibility
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:29Z
---

# The all-upstreams guarantee is enforced at dispatch eligibility

Supported. `lib/runtime/gate_evaluator.go::anySubscribedUpstreamInFlight` (tagged `@decision: upstream-gating-at-eligibility`) is one chokepoint conjunct of the gate evaluator's pending→stale predicate (per `concept:wait-set`'s three-conjunct rule), consulted regardless of which propagation path made the receiver stale — there is no separate pessimistic-seeding step at any of the walk-side stale-transition sites. The wait-set ledger's drained-rows substitution role is unchanged (`lib/runtime/substitution_context.go::pinnedSenderRunsForReceiver`). Self-edge exemption and cycle handling are checked directly: `test/scenarios/self_edge_attribute_carry_forward_test.go` and the "drain my own queue" idiom's own-in-flight exemption from the upstream conjunct (`concept:wait-set`'s conjunct (b) exemption) are exercised alongside the deterministic-diamond coverage of `test/scenarios/all_upstream_gating_test.go` (see `story:all-upstream-gating`'s audit for the diamond details).
