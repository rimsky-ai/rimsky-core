---
decision: sequence-scope-monotonic
status: as-is
aliases: []
---

# Node-run sequence is monotonic per (node, run-scope), not per (node, run-scope, frame)

## Choice

A node-run's sequence field is assigned at row creation as the largest existing sequence for the same `(node_id, run_scope_id)` plus one. Frame identity does not participate in the assignment. Multiple node-runs of the same node within the same scope across different frames receive strictly increasing sequence numbers.

## Rationale

The latest-run lookup that operator surfaces and the dispatcher's predecessor probes rely on must be deterministic. With per-(node, scope) monotonicity, "the latest run for this node in this scope" has a unique answer: the row with the largest sequence value. The latest-run-by-state probe orders by terminal-vs-in-flight rank and then by sequence descending, returning a single row.

Per-(node, scope, frame) monotonicity is insufficient. Two terminal runs in different frames can both receive sequence=1 — each frame's max-and-add-one subquery sees the other frame's rows as out of scope — and the latest-run lookup has no further tiebreaker. The lookup returns either row at the database's discretion, breaking the deterministic-latest-run contract operator surfaces depend on. The symptom surfaces as nondeterministic operator-visible state: a node whose latest run finished `fresh` may report its older `failed` run as the latest, with no visible difference between calls.

Per-scope monotonicity preserves the "Nth run of this node in this scope" semantic that sub-graph and fan-out RunScopes (per `concept:run-scope`) rely on for their own independent numbering — runs of the same node in sibling scopes do not collide.

## Alternatives

Per-(node, scope, frame) monotonicity with a wall-clock tiebreaker on the latest-run query — rejected. The tiebreaker would have to be a terminal timestamp, which is null for in-flight rows, requiring a COALESCE with the enqueued-at timestamp. Both timestamps are clock-dependent and ambiguous under clock skew or within a single millisecond resolution. A deterministic ordinal is preferable to a fragile clock-based ordering at this layer.

Per-(node, scope, frame) monotonicity with a join to frame creation order — rejected. The join makes the latest-run lookup more expensive on every operator probe and every dispatcher predecessor read, and frame creation order may not reflect intended run ordering when frames are constructed in parallel.

Globally monotonic sequence (per node only, ignoring scope) — rejected. Sub-graph and fan-out partition scopes carry their own independent execution semantics; their run numbering should not be perturbed by runs in sibling scopes or the parent scope. Per-(node, scope) preserves scope locality of the sequence axis.
