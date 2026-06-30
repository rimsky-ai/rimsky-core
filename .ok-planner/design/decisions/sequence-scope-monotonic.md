---
decision: sequence-scope-monotonic
status: as-is
aliases: []
---

# Node-run sequence is monotonic per (node, run-scope)

## Choice

A node-run's sequence field is assigned at row creation as the largest existing sequence for the same `(node_id, run_scope_id)` plus one. Sub-graph and fan-out partition RunScopes (per `concept:run-scope`) carry their own independent sequence numbering — runs of the same node in sibling RunScopes do not collide.

## Rationale

Per-scope monotonicity gives the latest-run lookup that operator surfaces and the dispatcher's predecessor probes a deterministic answer: the row with the largest sequence value. The latest-run-by-state probe orders by terminal-vs-in-flight rank and then by sequence descending, returning a single row.

Because a RunScope lives in exactly one frame (per `concept:run-scope`), the per-(node, scope, frame) form is equivalent to the per-(node, scope) form — the frame qualifier is redundant. Per-(node, scope) is chosen as the simplest sufficient form.

Per-scope monotonicity preserves the "Nth run of this node in this scope" semantic that sub-graph and fan-out RunScopes rely on for their own independent numbering — runs of the same node in sibling scopes do not collide.

## Alternatives

Globally monotonic sequence (per node only, ignoring scope) — rejected. Sub-graph and fan-out partition scopes carry their own independent execution semantics; their run numbering should not be perturbed by runs in sibling scopes or the parent scope. Per-(node, scope) preserves scope locality of the sequence axis.
