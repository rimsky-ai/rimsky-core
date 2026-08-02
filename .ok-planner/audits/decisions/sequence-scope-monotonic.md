---
audit: sequence-scope-monotonic
artifact: decision:sequence-scope-monotonic
determination: supported
commit: b767a27d
audited: 2026-08-02T09:43:58Z
---

# Node-run sequence is MAX(sequence)+1 per (node_id, run_scope_id) on both backends

Supported. Both the postgres and the sqlite `CreateCascadePending` inserts (the two backend implementations of cascade node-run creation, the full population of this insert path) compute the new row's sequence as `COALESCE((SELECT MAX(sequence) FROM rimsky_node_runs WHERE node_id = ? AND run_scope_id = ?), 0) + 1`, each tagged `@decision: sequence-scope-monotonic`. The backend-agnostic conformance suite's `testScopeKeyedOps_GetPriorRunBySequence` (run against both drivers) seeds two sibling run-scopes for the same node and confirms each scope's sequence-ordered lookups resolve independently, demonstrating sibling scopes don't perturb each other's numbering as the decision claims.
