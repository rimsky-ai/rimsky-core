---
concept: delegation
status: as-is
aliases: []
---

# Delegation

## Definition

Delegation is an invocation pattern over `concept:child-execution`: a node targeting a named sub-graph (instead of declaring its own executor) dispatches the sub-graph's internal nodes as one shared child execution context under the **carry** settle primitive (see `concept:child-execution`), with the entry absorbed. The calling node IS the sub-graph's entry:

- **The entry node is absorbed into the calling node.** At canonicalization, the calling node inherits the entry's executor, claim-producer bindings, holds bindings, and attribute schema, merged with what the calling node declared externally (other entry-declared fields do not carry over). The entry does not get its own node — it IS the calling node. Same node, same executor, same parent run. The calling node's run remains in the parent execution context (per `concept:run-scope`); the sub-graph's other internal nodes (exit plus any intermediates) run in the child execution context that the dispatch primitive allocates.
- **The exit node is NOT absorbed.** It is its own node (shared declaratively across invocations of this sub-graph in this instance) and runs inside the child execution context. At exit's terminal, settlement copies exit's writeback verbatim onto the calling node's attribute row and closes the child context — the carry settle primitive of `concept:child-execution`.

So entry absorption is structural; exit carry-up is the carry settle primitive of `concept:child-execution`. Delegation does not involve an aggregation policy — the policy enum is fan-out's knob (see `concept:fan-out`); the settle primitive is chosen by the invocation pattern, not configured.

Delegation has nothing structurally to do with fan-out; a delegating node may itself be a fan-out clone (composition layers them). Each fan-out partition clone absorbs the entry independently and opens its own child execution context for the sub-graph, so N fan-out partitions produce N independent sub-graph invocations, each with its own carry settle at its own exit — not one shared invocation.

## Boundaries

Owns: the template surface that targets a sub-graph for delegation, entry absorption at canonicalization (the genuine asymmetry versus fan-out), and triggering the entry-success internal cascade that begins the sub-graph's internal walk. Does NOT own: the dispatch and settlement shape, context closure, or the carry's atomicity — those belong to `concept:child-execution`; sub-graph template surface (see `concept:sub-graph`); per-run state aggregation (see `concept:node-run`). Adjacent: `concept:child-execution`, `concept:sub-graph`, `concept:node`, `concept:node-run`, `concept:run-scope` (the delegate runs in a sub-graph RunScope), `concept:cascade`.

## Invariants

- A node declares either an inline executor or a delegate target, not both; declaring both is rejected at template validation.
- The delegate target MUST be a sub-graph (with both entry and exit declared) reachable in the template; an unknown or non-sub-graph target is rejected.
- Entry absorption is computed at canonicalization deterministically; the calling node's executor is taken from the entry (and the conflict above is enforced).
- Delegation dispatches the sub-graph's internal nodes as one shared child execution context — one partition, N children; the carry settle primitive fires exactly once, on the designated exit's terminal, atomic with child-context closure — invariants of `concept:child-execution` (invariant: exit-node-writeback).
- Subscription edges from internal nodes referencing the entry alias resolve to the calling node per-invocation at the cascade walker level; this is what makes the absorption work across invocations.
- A node may declare both a delegate target and fan-out; the two are not mutually exclusive. Fan-out clones the calling node first, and each clone independently absorbs the entry and dispatches its own sub-graph invocation — the aggregation policy resolves over each partition's carried exit outcome, not over any shared sub-graph state.
- Delegate settlement resolves the calling node's own claims on every outcome: the carried exit success commits them; an aggregated sub-graph failure abandons them. No settlement outcome leaves the caller's claims dangling active.
- The entry-alias resolution is live at runtime, not just structural: an internal node's attribute substitutions referencing the entry alias read the calling node's per-invocation attribute bag, and the wait-set rows for entry-alias subscriptions name the calling node's run as sender.
