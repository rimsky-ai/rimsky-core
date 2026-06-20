---
concept: delegation
status: as-is
aliases: []
---

# Delegation

## Definition

Delegation is an invocation pattern over `concept:child-execution`: a node targeting a named sub-graph (instead of declaring its own executor) dispatches that sub-graph as exactly one child execution under the **carry** settlement mode (see `concept:child-execution`), with the entry absorbed. The calling node IS the sub-graph's entry:

- **The entry node is absorbed into the calling node.** At canonicalization, the calling node inherits the entry's executor and the entry's sub-graph-internal declarations merged with what the calling node declared externally. The entry does not get its own node — it IS the calling node. Same node, same executor, same parent run. The calling node's run remains in the parent execution context (per `concept:run-scope`); the sub-graph's other internal nodes (exit plus any intermediates) run in the child execution context that the dispatch primitive allocates.
- **The exit node is NOT absorbed.** It is its own node (shared declaratively across invocations of this sub-graph in this instance) and runs inside the child execution context. At exit's terminal, settlement copies exit's writeback verbatim onto the calling node's attribute row and closes the child context — the carry settlement mode of `concept:child-execution`.

So entry absorption is structural; exit carry-up is the carry settlement mode of `concept:child-execution`. Delegation does not involve an aggregation policy — the policy enum is fan-out's knob (see `concept:fan-out`); the settlement mode is chosen by the invocation pattern, not configured.

## Boundaries

Owns: the template surface that targets a sub-graph for delegation, entry absorption at canonicalization (the genuine asymmetry versus fan-out), and the running-to-running transition reason for a sub-graph-internal cascade firing (see `concept:transition-reason`). Does NOT own: the dispatch and settlement shape, context closure, or the carry's atomicity — those belong to `concept:child-execution`; sub-graph template surface (see `concept:sub-graph`); per-run state aggregation (see `concept:node-run`); cascade-boundary opacity (see `concept:cascade`). Adjacent: `concept:child-execution`, `concept:sub-graph`, `concept:node`, `concept:node-run`, `concept:cascade`.

## Invariants

- A node declares either an inline executor or a delegate target, not both; declaring both is rejected at template validation.
- The delegate target MUST be a sub-graph (with both entry and exit declared) reachable in the template; an unknown or non-sub-graph target is rejected.
- Entry absorption is computed at canonicalization deterministically; the calling node's executor is taken from the entry (and the conflict above is enforced).
- Delegation always dispatches exactly one child under the carry settlement mode; the carry mode's one-child requirement and the carry's atomicity with child-context closure are invariants of `concept:child-execution` (invariant: exit-node-writeback).
- Subscription edges from internal nodes referencing the entry alias resolve to the calling node per-invocation at the cascade walker level; this is what makes the absorption work across invocations.
