---
audit: all-upstream-gating
artifact: story:all-upstream-gating
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:29Z
---

# Template author relies on all-upstream gating for fan-in shapes

Supported. `lib/runtime/gate_evaluator.go::anySubscribedUpstreamInFlight` implements the eligibility-side conjunct: a stale transition is blocked while any subscribed upstream (found by walking the subscription-edge map, not by any per-path bookkeeping) has an in-flight run in the same frame, for both terminal-edge and attribute-changed-edge subscriptions alike. `test/scenarios/all_upstream_gating_test.go::TestAllUpstreamGating_DiamondSettlementPropagated` proves this on a diamond (`d` fans in from `b` and `c`, each subscribed via both a terminal wildcard and an attribute-changed edge): while `c` is deliberately held in-flight, the test polls and asserts zero `d` rows reach `stale`/`running`/`held`/`parked` and `d`'s dispatch count stays at baseline; once `c` is released, `d` is asserted to dispatch exactly once (not once per settling sender) with substitution values from both `b`'s and `c`'s latest-in-frame contributions.
