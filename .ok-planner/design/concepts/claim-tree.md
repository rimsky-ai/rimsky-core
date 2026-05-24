---
concept: claim-tree
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Claim tree

## Definition

The tree-shaped relationship across `rimsky_claim_handles` rows, formed by the nullable `parent_claim_handle_id` column. A root claim handle has `parent_claim_handle_id IS NULL`; a sub-claim has `parent_claim_handle_id = <parent's id>`. The structure mirrors the run-tree on `rimsky_node_runs` (which uses `run_scope_id` per `concept:run-scope`, with the parent-child shape on `rimsky_run_scopes` rather than inline on the node_run row) but exists at the claim layer rather than the dispatch layer. Created by fan-out: the parent's `SplitScope` returns N sub-scope descriptors and rimsky inserts N child claim_handle rows in the same acquisition transaction.

Used by the auto-terminal recursion in `runtime/auto_terminal.go::resolveParentClaimChain`: when a child claim resolves, the recursive walker reads the parent's children via `ListChildClaimHandles(parent_id)`, computes the parent's aggregate verdict per its snapshotted `aggregation_policy` (see `concept:fan-out` + `concept:node-run`), and fires the parent's own terminal — which itself may walk further up to a grandparent.

## Boundaries

Owns: the `parent_claim_handle_id` FK on `rimsky_claim_handles`, the `ListChildClaimHandles` accessor, the recursive `resolveParentClaimChain` walk, the recursive descendant-cancel walk (`cancelDescendantClaims`) used by `concept:cancel-siblings`. Does NOT own: claim acquisition (see `concept:claim`, `concept:claim-handle`), state aggregation policy (see `concept:fan-out`), the run-tree (see `concept:node-run`). Adjacent: `concept:claim-handle`, `concept:fan-out`, `concept:cancel-siblings`, `concept:auto-terminal`, `concept:node-run`.

## Invariants

- The FK uses `ON DELETE SET NULL` on `parent_claim_handle_id` so a parent's deletion does not cascade-delete its in-flight children. Children that survive their parent's deletion become orphaned in-flight; the recursive descendant-cancel walk (`cancelDescendantClaims`) fires BEFORE the parent's own Delete to avoid this for the Abandon case.
- Each non-root claim_handle row is reachable from exactly one root via the parent chain. The tree shape is enforced structurally (single parent FK column).
- The recursive walker terminates because each Delete strictly reduces the tree size; bounded by claim-tree depth.
- The parent's aggregation counters (`expected_children_count`, `committed_children_count`, `abandoned_children_count` per `migration 007`) are claimant-guarded — bumped only by the supervisor that holds the parent. See `@blessed-invariant 4`.
- For terminal children (committed or abandoned), the row is preserved by `Promote(committed|abandoned)` and participates in the parent's aggregation counter; the descendant-cancel walker skips all non-`active` rows (`state != 'active'`), so committed-durable children preserve the durable-Commit contract (no force-Abandon undoes a successful promotion) and committed-subgraph + abandoned rows aren't candidates for re-cancellation either.

## Annotation sites

- `code:runtime/terminal_decision.go::ResolveClaimHandleTerminal` — the entry point for the recursive walk.
- `code:runtime/auto_terminal.go::resolveParentClaimChain` — the parent-walk recursion.
- `code:runtime/terminal_decision.go::cancelDescendantClaims` — the descendant-walk recursion.
- `code:runtime/runner_subclaim.go::AcquireSubClaims` — sub-claim INSERT site (creates the tree edges).
- `code:runtime/fanout_dispatch.go::dispatchFanOutChildren` — leaf-run dispatch over the tree.
- `code:runtime/data_processing.go` — DataProcessing dispatch dimensions sub-claims.
- `code:foundation/persistence/claim_handles.go::ListChildClaimHandles` — the table accessor.
- `code:test/scenarios/run_tree/` — tree-resolution scenarios.
- `code:test/scenarios/forensics/fanout_post_mortem_test.go` — post-mortem traversal.
- `code:test/scenarios/lineage/claim_abandon_lineage_test.go` and `force_cancelled_lineage_test.go`.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The naming "claim-tree" is internal — the persisted shape is a self-referential FK on `rimsky_claim_handles`, not a separate tree table. Recursion is bounded by structure, not by depth-limit configuration; deeply nested fan-out (fan-out of fan-out) is supported and exercised by `TestResolveParentClaimChain_StrictCancelSiblings_RecursivelyCancelsGrandchildren`.

State-column refactor per `spec:2026-05-17-post-data-platform-cleanup`: the descendant-cancel walker now uses `state != 'active'` as its skip filter (replacing the historical `held_durable = TRUE`). Functionally identical because (a) committed-durable rows have `state = committed`; (b) committed-subgraph and abandoned rows likewise aren't `active` and shouldn't be re-cancelled.

2026-05-22 — Updated cross-reference to reflect the run-tree shape change per spec `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md`. Claim-tree (`parent_claim_handle_id` on `rimsky_claim_handles`) and RunScope-tree (`parent_run_scope_id` on `rimsky_run_scopes`) are now both first-class trees at the persistence layer; they remain parallel structures owned by different concepts.
