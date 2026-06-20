---
concept: child-execution
status: as-is
aliases: []
---

# Child execution

## Definition

Child execution is the run-side primitive by which a parent node-run dispatches one or more child executions into their own execution contexts and settles on their aggregate outcome. It is a primitive pair:

- **Dispatch-children** takes N≥1 partition descriptors (partition key, optional sub-claim handle, inert payload) and a child graph name, and dispatches one child execution per partition into its own child execution context rooted at the parent run.
- **Settle-children** fires under the **settlement mode** the invocation pattern selected. Two settlement modes exist:
  - **Carry** — the subgraph-exit path used by delegation. Fires once when the sub-graph's exit node terminates: copies the exit's writeback onto the calling node's attribute row, closes the child run-scope, fires the parent-settlement cascade.
  - **Aggregate** — the fan-out path used per child as each sub-claim resolves. Each call records the child's outcome on the parent claim and applies the aggregation policy; when the policy settles the parent, the call also closes the remaining partition contexts, settles the parent's claim, and fires the parent-settlement cascade.

Delegation and fan-out are invocation patterns over this primitive (see `concept:delegation`, `concept:fan-out`): delegation is one partition under the **carry** settlement mode with an absorbed entry; fan-out is N partitions under the **aggregate** settlement mode with an author-chosen aggregation policy and one sub-claim per partition. The settlement mode is implicit in the invocation pattern; authors do not configure it.

## Purpose

Own the shared dispatch shape and the parent-settlement cascade bridge so the two invocation patterns route through one dispatch and a common settlement framing. A defect fixed in the primitive is fixed for both patterns; an invariant enforced in the primitive cannot be skipped by either pattern.

## Boundaries

Owns the dispatch primitive (N≥1 children into child execution contexts) and the two settlement modes (carry, aggregate) with their shared cascade-bridge mechanic. The execution contexts themselves and their tree structure are owned by `concept:run-scope`. Template surfaces are owned by `concept:delegation` and `concept:fan-out`. The aggregation policy — the fan-out-only knob with four values (`strict | threshold | best_effort | first`) — is owned by `concept:fan-out`. Sub-claim acquisition is owned by `concept:claim-tree` — the dispatch primitive accepts already-acquired sub-claims as input and never calls the producer's split itself. Adjacent: `concept:run-scope`, `concept:delegation`, `concept:fan-out`, `concept:claim-tree`, `concept:node-run`, `concept:cascade`.

## Invariants

- Settlement is the only run-side path that closes child execution contexts (instance termination is the administrative exception, per `concept:run-scope`).
- The carry settlement mode requires exactly one child, enforced at template validation; a delegation declaration that would dispatch more than one child is a template-validation error.
- Entry absorption is a property of the invoking pattern, not of child execution — the dispatch primitive carries it as an input flag and does not compute it.
- The parent-settlement cascade cannot be skipped by any settlement caller: the cascade bridge fires inside the settlement primitive, not alongside it at call sites.
- The settlement's outcome carry — the carry mode's exit-attribute writeback or the aggregate mode's parent-claim Commit/Abandon — is atomic with closing the child execution context (invariant: exit-node-writeback).
