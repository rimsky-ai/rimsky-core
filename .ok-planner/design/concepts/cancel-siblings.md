---
concept: cancel-siblings
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Cancel siblings

## Definition

A boolean field on the `strict` aggregation policy (`AggregationPolicy.CancelSiblings`) that turns on proactive sibling cancellation: when one sub-claim resolves to `AggregateAbandon` under a parent whose policy is `strict + cancel_siblings: true`, the runtime walks the parent's other in-flight sub-claims and force-Abandons each via recursive `ResolveClaimHandleTerminal` calls. Realizes "fail fast" for `strict` aggregation.

Declared on a fan-out parent's `error_policy: { strict: { cancel_siblings: true } }`. Snapshotted on the parent claim_handle row at acquire-time (`aggregation_policy JSONB` column, `migration 007`). Implemented in `runtime/terminal_decision.go::cancelInFlightSiblings` (sibling level) + `cancelDescendantClaims` (recursive descent — see `concept:claim-tree`).

## Boundaries

Owns: the `CancelSiblings` policy field, the proactive cancel walker, the recursive descendant cascade, the multi-supervisor scope filter. Does NOT own: the post-resolution aggregate verdict (see `concept:fan-out` aggregator), the `strict` policy itself (see `concept:node-run` aggregation table), the held-durable promotion (see `concept:claim-lifetime`). Adjacent: `concept:claim-tree`, `concept:fan-out`, `concept:claim-co-holdership`, `concept:claim-lifetime`.

## Invariants

- Proactive cancellation fires inside the triggering child's `ResolveClaimHandleTerminal` call, AFTER the producer Abandon verb and the parent-counter bump, BEFORE the parent's recursive resolution walk.
- Each force-Abandoned sibling is force-Abandoned via the same `ResolveClaimHandleTerminal` path; the recursion runs the standard counter-bump + lineage-write + cancel-descendants chain, so the parent's aggregate verdict ends up consistent regardless of how many siblings were force-cancelled.
- The recursion is bounded by claim-tree depth, not configuration. A force-Abandoned sibling that is itself a fan-out parent has its grandchildren cancelled via `cancelDescendantClaims` running inside the recursive `ResolveClaimHandleTerminal` frame BEFORE the sibling's own Delete fires (so the FK `parent_claim_handle_id ON DELETE SET NULL` doesn't orphan the grandchildren).
- Each sibling row is `SELECT … FOR UPDATE`-locked before the recursive cancellation fires on it. The lock is held for the duration of the recursive call. This closes the race against a parallel worker on the same supervisor that may be terminating the sibling natively (Commit/Abandon via the executor path) — producer-side `claim_id` idempotency cannot reconcile distinct verbs (Commit vs Abandon).
- Force-cancelled rows are written to `rimsky_lineage` with `outcome: 'force_cancelled'` and a `cause` field distinguishing `sibling_cancel` from `descendant_cancel` — see `concept:lineage`.
- The cancel walker SKIPS three classes of sibling rows: (a) non-active rows (`state != 'active'`) — committed-durable rows preserve the `concept:claim-lifetime` durable-Commit contract; committed-subgraph and abandoned rows aren't candidates for re-cancellation either; (b) rows already Promoted by an inner recursive walker (defensive re-check after `LockForUpdate`); (c) **rows held by a different supervisor** (`holder_supervisor_id != args.SupervisorID`), per `@blessed-invariant 4` (claimant-guarded release).
- The parent claim_handle row is also gated on `parent.State == active` — symmetric with other auto-terminal paths (`CheckAndFireResolution`, `resolveParentClaimChain`). Non-active parents have already resolved.
- Malformed `aggregation_policy` JSONB on the parent → log a warn and treat as no-cancel (safe fallback; the post-resolution aggregator's default-strict path still computes a correct aggregate verdict).

## Multi-supervisor scope (load-bearing)

**Cancel-siblings is scoped to the supervisor that holds the parent.** Under multi-supervisor deployments (replicas > 1 on the supervisor StatefulSet), sub-claims of the same parent can be acquired by different supervisor processes. The cancel walker filters mismatched-supervisor siblings out of its walk per `@blessed-invariant 4`: a supervisor cannot release claims held by a different supervisor.

Practical consequence under `strict.cancel_siblings: true` + multi-supervisor fan-out:

- Supervisor A holds 5 of 12 sub-claims; supervisor B holds the other 7.
- One of A's sub-claims resolves to Abandon.
- A's cancel walker force-Abandons A's other 4 sub-claims.
- A's cancel walker SKIPS B's 7 sub-claims (claimant-guard filter).
- B's 7 sub-claims continue to natural completion; each Commit / Abandon bumps the parent counter independently.
- The parent's aggregator computes the final verdict from the union of A's force-Abandons + B's natural outcomes.

"Fail fast" is honored within a supervisor, not across. The producer side is also protected: forcing an Abandon from supervisor A on a claim held by supervisor B would race with B's natural Commit/Abandon and corrupt the producer's `claim_id`-keyed state.

Fan-out is typically single-supervisor in practice (the supervisor that acquired the parent dispatches the children; single-replica deployments are the common case). The multi-supervisor edge case matters when (a) `replicas > 1`, AND (b) multiple supervisors picked up sibling sub-claim rows in parallel, AND (c) `strict.cancel_siblings: true` is set on the parent.

## Annotation sites

- `code:runtime/terminal_decision.go::cancelInFlightSiblings` — the sibling-level walk.
- `code:runtime/terminal_decision.go::cancelDescendantClaims` — the descendant-tree walk.
- `code:runtime/auto_terminal.go::resolveParentClaimChain` — the parent recursion that follows the walk.
- `code:foundation/spec/aggregation_policy.go` — `AggregationPolicy.CancelSiblings` field + persistence.
- `code:foundation/persistence/postgres/migrations/007-claim-handles-parent-aggregation.sql` — the column snapshot.
- `code:runtime/auto_terminal_test.go::TestResolveParentClaimChain_StrictCancelSiblings_*` — three scenarios (basic cancel + skips-durable-sibling + recursive grandchildren).
- `code:test/scenarios/lineage/force_cancelled_lineage_test.go` — lineage assertion.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md` §Error policy. The recursive-descent variant (descendants of force-Abandoned siblings get force-Abandoned too) landed in cleanup-cycle 9 after the cycle-8 single-level implementation was reviewed as spec-violating per §435. The multi-supervisor scope is a documented intentional limitation; if cross-supervisor cancellation is ever needed, options are (a) DB-mediated "please abandon" signal that the other supervisor's terminal handler reads on tick, or (b) producer-side multi-supervisor coordination on `claim_id`. Neither is implemented.

State-column refactor per `spec:2026-05-17-post-data-platform-cleanup`: the skip filter changed from `held_durable = TRUE` to `state != 'active'`. The post-refactor filter is strictly broader (also skips committed-subgraph and abandoned rows) but the behavior is identical because (a) only active rows are cancellation candidates in the first place; (b) the pre-refactor cancel path went through `Delete` which would have no-op'd on already-deleted committed-subgraph or abandoned rows.
