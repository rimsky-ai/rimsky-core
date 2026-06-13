---
concept: delegation
status: as-is
aliases: []
---

# Delegation

## Definition

Delegation is an invocation pattern over `concept:child-execution`: a node carrying `delegate: <graph-name>` instead of `executor:` dispatches the named sub-graph as exactly one child execution — one child, the carry-verbatim aggregation policy, entry absorbed. The calling node IS the sub-graph's entry:

- **The entry node is absorbed into the calling node.** At canonicalization, the calling node's persisted node row inherits the entry node's executor and the entry node's sub-graph-internal declarations (claims/holds/userdata) merged with what the calling node declared externally. The entry node does NOT get its own node row — it IS the calling node. Same row, same executor, same parent run. The calling node's run remains in the parent execution context (per `concept:run-scope`); the sub-graph's other internal nodes (exit + any intermediates) run in the child execution context that the dispatch primitive allocates.
- **The exit node is NOT absorbed.** It has its own persisted node row (shared declaratively across invocations of this sub-graph in this instance) and runs inside the child execution context. At exit's terminal, settlement carries exit's writeback verbatim to the calling node's parent-run writeback and closes the child context — the carry-verbatim shape of `concept:child-execution` settlement.

So entry absorption is structural; exit carry-up is the carry-verbatim settlement policy.

## Boundaries

Owns: the `delegate:` template surface, entry absorption at canonicalization (the genuine asymmetry versus fan-out), and the `running → running` transition reason for a sub-graph-internal cascade firing (see `concept:transition-reason`). Does NOT own: the dispatch and settlement shape, context closure, or the carry's atomicity — those belong to `concept:child-execution`; sub-graph template-DSL (see `concept:sub-graph`); per-run state aggregation (see `concept:node-run`); cascade-boundary opacity (see `concept:cascade`). Adjacent: `concept:child-execution`, `concept:sub-graph`, `concept:node`, `concept:node-run`, `concept:cascade`.

## Invariants

- A node has either `executor:` or `delegate:`, not both. Both → reject `node_executor_and_delegate`.
- The delegate target MUST be a sub-graph (must have `entry:` + `exit:`) declared in the template's `graphs:` block. Missing graph or `main` target → reject.
- Entry absorption is computed at canonicalization deterministically; the calling node's `executor:` field is overwritten with the entry's executor (and conflict-rejected if the calling node also declared `executor:` per the rule above).
- Delegation always dispatches exactly one child under the carry-verbatim policy; carry-verbatim's one-child requirement and the carry's atomicity with child-context closure are invariants of `concept:child-execution` (`@blessed-invariant: exit-node-writeback`).
- Subscription edges from internal nodes referencing the entry alias resolve to the calling node per-invocation at the cascade walker level; this is what makes the absorption work across invocations.
