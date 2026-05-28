---
concept: delegation
status: as-is
aliases: []
---

# Delegation

## Definition

Delegation is the relationship between a calling node and a sub-graph: a node carries `delegate: <graph-name>` instead of `executor:`, and dispatches the named sub-graph as its execution unit. Identity is asymmetric:

- **The entry node is absorbed into the calling node.** At canonicalization, the calling node's persisted node row inherits the entry node's executor and the entry node's sub-graph-internal declarations (claims/holds/userdata) merged with what the calling node declared externally. The entry node does NOT get its own node row — it IS the calling node. Same row, same executor, same parent run. The calling node's run remains in the parent RunScope (per `concept:run-scope`). The sub-graph's other internal nodes (exit + any intermediates) run in a **sub-graph RunScope** whose parent run is the calling node's run, whose parent run-scope is the calling node's RunScope, and whose graph name is the delegate target.
- **The exit node is NOT absorbed.** It has its own persisted node row (shared declaratively across invocations of this sub-graph in this instance) and runs inside the sub-graph RunScope. The carry-rule fires at exit's leaf-run terminal: the supervisor copies exit's writeback to the calling node's parent-run writeback in the same transaction, and atomically closes the sub-graph RunScope (stamping its closed-at timestamp).

So entry absorption is structural; exit carry-up is conceptual.

## Boundaries

Owns: the calling-node ↔ sub-graph relationship, entry absorption at canonicalization, the exit-node writeback carry-rule, the per-invocation run-tree shape, the `running → running` transition reason for a sub-graph-internal cascade firing (see `concept:transition-reason`). Does NOT own: sub-graph template-DSL (see `concept:sub-graph`), per-run state aggregation (see `concept:node-run`), cascade-boundary opacity (see `concept:cascade` post-2026-05-15 update). Adjacent: `concept:sub-graph`, `concept:node`, `concept:node-run`, `concept:cascade`.

## Invariants

- A node has either `executor:` or `delegate:`, not both. Both → reject `node_executor_and_delegate`.
- The delegate target MUST be a sub-graph (must have `entry:` + `exit:`) declared in the template's `graphs:` block. Missing graph or `main` target → reject.
- Entry absorption is computed at canonicalization deterministically; the calling node's `executor:` field is overwritten with the entry's executor (and conflict-rejected if the calling node also declared `executor:` per the rule above).
- The exit-writeback carry-rule is mandatory: at exit's terminal, the carry-rule runs in the same transaction that records exit's terminal. Per `@blessed-invariant 23`.
- Subscription edges from internal nodes referencing the entry alias resolve to the calling node per-invocation at the cascade walker level; this is what makes the absorption work across invocations.

## Notes

Introduced by `spec:2026-05-15-data-platform-extensions-design`. The asymmetric identity is what lets delegation feel like a function call from outside (entry IS the calling node) while exit terminals contribute to the calling node's writeback via a uniform aggregation rule.

2026-05-22 — Reshape per `spec:2026-05-22-fan-out-safety-scope-first-design`: sub-graph internal nodes now live in a sub-graph RunScope (`concept:run-scope`) created at the calling-node success terminal; carry-rule closure semantics added (exit-writeback also closes the sub-graph RunScope atomically).

- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
