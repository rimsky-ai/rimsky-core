---
decision: sequence-scope-monotonic
---

# Node-run sequence is monotonic per (node, run-scope)

## Choice

A node-run's sequence field is assigned at row creation as the largest existing sequence for the same `(node_id, run_scope_id)` plus one. Sub-graph and fan-out partition RunScopes (per `concept:run-scope`) carry their own independent sequence numbering — runs of the same node in sibling RunScopes do not collide.

## Rationale

Per-scope monotonicity gives the latest-run lookup that operator surfaces and the dispatcher's predecessor probes a deterministic answer — the row with the largest sequence — and preserves the "Nth run of this node in this scope" semantic that sub-graph and fan-out RunScopes rely on. Because a RunScope lives in exactly one frame (per `concept:run-scope`), a per-(node, scope, frame) form would be equivalent — the frame qualifier is redundant — so per-(node, scope) is the simplest sufficient form.

## Alternatives

- Globally monotonic sequence per node, ignoring scope — rejected: sub-graph and fan-out partition scopes carry their own independent execution semantics; their run numbering should not be perturbed by runs in sibling scopes or the parent scope.
