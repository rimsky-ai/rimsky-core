---
audit: cascade-mode
artifact: concept:cascade-mode
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T04:47:34Z
---

# The four cascade modes and their rules at the gate evaluator's pending-to-stale transition

Supported. The mode enum carries exactly the four values the concept names, is validated at template registration (an unknown value is rejected, an unset one accepted), and is read per node inside the gate evaluator, which applies the mode rule at precisely the pending-to-stale transition and before the advanced-sibling check, as the concept's most-recent row requires. Each of the four rules matches: most-recent deletes prior unclaimed cascade-driven stale rows for the same node and run scope; sequenced takes no action, leaving the uniform at-most-one-past-pending check to serialise rounds; both idempotent modes canonicalise the resolved bag to stable bytes and compare it against the most recent prior unclaimed cascade row, with idempotent-settled additionally falling back to the most recent settled successful run when no such prior exists. All six queries backing those rules key on node plus run scope with no frame predicate, which is what makes the intra-frame invariant structural rather than enforced — run scopes never span frames — and the mode rules run only from the pending-row path, so the three non-cascade creation reasons (of the four-value creation-reason enum) are created directly stale and never reach them. The three stories the concept names all exist in the catalog, and scenario tests exercise the sequenced and both idempotent behaviours end to end. One behaviour the concept's table omits: under most-recent the transitioning row is itself dropped when a later unclaimed cascade row already exists for the same node and scope, a fourth action alongside the three the opening section enumerates.
