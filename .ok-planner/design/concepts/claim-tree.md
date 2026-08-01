---
concept: claim-tree
status: as-is
aliases: []
---

# Claim tree

## What it is

The tree-shaped relationship across claim handle rows, formed by the nullable self-referential parent pointer. A root claim handle has a null parent pointer; a sub-claim points at its parent's id. The structure mirrors the run-tree (which lives at the run-scope layer per `concept:run-scope`, with the parent-child shape on the run-scope ledger) but exists at the claim layer rather than the dispatch layer. Created by fan-out: the parent's split-scope verb returns N sub-scope descriptors and rimsky inserts N child claim-handle rows in the same acquisition transaction.

Used by the parent-resolution walk, part of the unified terminal-resolution engine (see `concept:terminal-resolution`): when a child claim resolves, the walk reads the parent's children, and only once every claim-holder row on the parent and every child claim-handle row is no longer active does it compute the parent's aggregate verdict per its snapshotted aggregation policy (see `concept:fan-out` + `concept:node-run`) and fire the parent's own terminal — which itself may walk further up to a grandparent. While any holder or child remains active, the walk records the child's outcome and stops.

## Boundaries

Owns: the self-referential parent pointer on the claim-handle ledger, the child-listing accessor, the recursive parent-resolution walk, the recursive descendant-cancel walk (fires unconditionally whenever any claim resolves to Abandon, independent of aggregation policy). Does NOT own: claim acquisition (see `concept:claim`, `concept:claim-handle`), state aggregation policy (see `concept:fan-out`), the run-tree (see `concept:node-run`), the proactive in-flight-sibling cancel that strict aggregation layers on top of a resolving child (see `concept:cancel-siblings`). Adjacent: `concept:claim-handle`, `concept:fan-out`, `concept:cancel-siblings`, `concept:auto-terminal`, `concept:terminal-resolution`, `concept:node-run`.

## Invariants

- The parent pointer nulls on a parent's deletion (rather than cascading) so a parent's deletion does not cascade-delete its in-flight children. The recursive descendant-cancel walk fires before the parent's own terminal resolution — promotion to abandoned, or deletion under the ownership-bail source — so descendants are not left orphaned in-flight. The walk is scoped to descendants held by the acting supervisor; a descendant held by a different supervisor is skipped and can remain active after the parent's abandon in multi-supervisor deployments (see `concept:cancel-siblings`'s multi-supervisor scope).
- Each non-root claim-handle row is reachable from exactly one root via the parent chain. The single parent pointer per row guarantees at most one parent; acyclicity, and with it the tree shape, is operational rather than structural — a row is only ever inserted pointing at a pre-existing parent under a freshly generated id, never rewired after insertion.
- Both recursive walks terminate because they are bounded by claim-tree depth. The descendant-cancel walk additionally shrinks its own frontier on every step: a resolved descendant leaves the active state (promoted, or deleted under the ownership-bail source) and the walk only ever recurses into rows still active, so no row is visited twice.
- The parent's aggregation counters (expected, committed, and abandoned child counts) are claimant-guarded (invariant 4): the mutation targets whichever supervisor currently holds the parent row at settlement time, not necessarily the supervisor that originally acquired it. A settling supervisor that does not yet hold the parent reassigns holdership to itself before firing the parent's own terminal resolution (see `concept:cancel-siblings`'s multi-supervisor scope for the sibling-cancellation side of this same boundary).
- For terminal children resolved through natural settlement or sibling-cancellation, the row is preserved by the promote transition and participates in the parent's aggregation counter. Children swept up by the descendant-cancel walk are preserved the same way but do not bump a parent's counter — their immediate parent is itself being torn down in the same walk, so there is no live aggregation left to feed. The descendant-cancel walk skips all non-active rows, so committed-durable children preserve the durable-Commit contract (no force-Abandon undoes a successful promotion) and committed-subgraph + abandoned rows aren't candidates for re-cancellation either.
