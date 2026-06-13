---
concept: child-execution
status: as-is
aliases: []
---

# Child execution

## Definition

Child execution is the run-side primitive by which a parent node-run dispatches one or more child executions into their own execution contexts and settles on their aggregate outcome. It is a primitive pair:

- **Dispatch-children** takes N≥1 partition descriptors (partition key, optional sub-claim handle, inert payload) plus an aggregation policy and a child graph name, and dispatches one child execution per partition into its own child execution context rooted at the parent run.
- **Settle-children** fires on every child terminal: it records the child's outcome, applies the aggregation policy (carry-verbatim, or the strict / threshold / best-effort / first family), and — when the policy settles the parent — closes the child execution context(s), writes the parent settlement, and fires the parent-settlement cascade from inside settlement.

Delegation and fan-out are invocation patterns over this primitive (see `concept:delegation`, `concept:fan-out`): delegation is one partition with the carry-verbatim policy and an absorbed entry; fan-out is N partitions with an author-specified policy and one sub-claim per partition. The run-side shape is one shape.

## Purpose

Own the shared shape that delegation and fan-out are surfaces of, so that there is exactly one dispatch path and exactly one settlement path on the run side. A defect fixed in the primitive is fixed for every invocation pattern; an invariant enforced in the primitive cannot be skipped by any pattern.

## Boundaries

Owns the dispatch primitive (N≥1 children into child execution contexts) and the settlement primitive (record child outcome → apply aggregation policy → close child contexts → settle parent, with the parent-settlement cascade fired from inside settlement). The execution contexts themselves and their tree structure are owned by `concept:run-scope`. Template surfaces are owned by `concept:delegation` and `concept:fan-out`. Sub-claim acquisition is owned by `concept:claim-tree` — the dispatch primitive accepts already-acquired sub-claims as input and never calls the producer's split itself. Adjacent: `concept:run-scope`, `concept:delegation`, `concept:fan-out`, `concept:claim-tree`, `concept:node-run`, `concept:cascade`.

## Invariants

- Settlement is the only run-side path that closes child execution contexts (instance termination is the administrative exception, per `concept:run-scope`).
- The carry-verbatim aggregation policy requires exactly one child, enforced at template validation; a declaration with multiple children under carry-verbatim is a template-validation error.
- Entry absorption is a property of the invoking pattern, not of child execution — the dispatch primitive carries it as an input flag and does not compute it.
- The parent-settlement cascade cannot be skipped by any settlement caller: the cascade bridge fires inside the settlement primitive, not alongside it at call sites.
- The settlement's outcome carry — writing the aggregated or carried-verbatim outcome back to the parent — is atomic with closing the child execution context (`@blessed-invariant: exit-node-writeback`).
