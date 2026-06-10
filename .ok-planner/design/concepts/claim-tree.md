---
concept: claim-tree
status: as-is
aliases: []
---

# Claim tree

## Definition

The tree-shaped relationship across claim handle rows, formed by the nullable self-referential parent pointer. A root claim handle has a null parent pointer; a sub-claim points at its parent's id. The structure mirrors the run-tree (which lives at the run-scope layer per `concept:run-scope`, with the parent-child shape on the run-scope ledger) but exists at the claim layer rather than the dispatch layer. Created by fan-out: the parent's split-scope verb returns N sub-scope descriptors and rimsky inserts N child claim-handle rows in the same acquisition transaction.

Used by the auto-terminal recursion: when a child claim resolves, the recursive walker reads the parent's children, computes the parent's aggregate verdict per its snapshotted aggregation policy (see `concept:fan-out` + `concept:node-run`), and fires the parent's own terminal — which itself may walk further up to a grandparent.

## Boundaries

Owns: the self-referential parent pointer on the claim-handle ledger, the child-listing accessor, the recursive parent-resolution walk, the recursive descendant-cancel walk used by `concept:cancel-siblings`. Does NOT own: claim acquisition (see `concept:claim`, `concept:claim-handle`), state aggregation policy (see `concept:fan-out`), the run-tree (see `concept:node-run`). Adjacent: `concept:claim-handle`, `concept:fan-out`, `concept:cancel-siblings`, `concept:auto-terminal`, `concept:node-run`.

## Invariants

- The parent pointer nulls on a parent's deletion (rather than cascading) so a parent's deletion does not cascade-delete its in-flight children. Children that survive their parent's deletion become orphaned in-flight; the recursive descendant-cancel walk fires BEFORE the parent's own delete to avoid this for the Abandon case.
- Each non-root claim-handle row is reachable from exactly one root via the parent chain. The tree shape is enforced structurally (a single parent pointer per row).
- The recursive walker terminates because each delete strictly reduces the tree size; bounded by claim-tree depth.
- The parent's aggregation counters (expected, committed, and abandoned child counts) are claimant-guarded — bumped only by the supervisor that holds the parent. See `@blessed-invariant 4`.
- For terminal children (committed or abandoned), the row is preserved by the promote transition and participates in the parent's aggregation counter; the descendant-cancel walker skips all non-active rows, so committed-durable children preserve the durable-Commit contract (no force-Abandon undoes a successful promotion) and committed-subgraph + abandoned rows aren't candidates for re-cancellation either.
