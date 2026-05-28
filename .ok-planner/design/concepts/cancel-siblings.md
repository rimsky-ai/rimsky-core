---
concept: cancel-siblings
status: as-is
aliases: []
---

# Cancel siblings

## Definition

A boolean field on the `strict` aggregation policy that turns on proactive sibling cancellation: when one sub-claim resolves to an aggregate-abandon under a parent whose policy is strict with cancel-siblings on, the runtime walks the parent's other in-flight sub-claims and force-Abandons each via recursive claim-handle terminal-resolution calls. Realizes "fail fast" for `strict` aggregation.

Declared on a fan-out parent's `error_policy: { strict: { cancel_siblings: true } }`. Snapshotted on the parent claim-handle row at acquire-time (in a JSON aggregation-policy column). Implemented as a sibling-level cancel walk plus a recursive descendant walk (see `concept:claim-tree`).

## Boundaries

Owns: the cancel-siblings policy field, the proactive cancel walker, the recursive descendant cascade, the multi-supervisor scope filter. Does NOT own: the post-resolution aggregate verdict (see `concept:fan-out` aggregator), the `strict` policy itself (see `concept:node-run` aggregation), the held-durable promotion (see `concept:claim-lifetime`). Adjacent: `concept:claim-tree`, `concept:fan-out`, `concept:claim-co-holdership`, `concept:claim-lifetime`.

## Invariants

- Proactive cancellation fires inside the triggering child's terminal-resolution call, AFTER the producer Abandon verb and the parent-counter bump, BEFORE the parent's recursive resolution walk.
- Each force-Abandoned sibling is force-Abandoned via the same terminal-resolution path; the recursion runs the standard counter-bump + lineage-write + cancel-descendants chain, so the parent's aggregate verdict ends up consistent regardless of how many siblings were force-cancelled.
- The recursion is bounded by claim-tree depth, not configuration. A force-Abandoned sibling that is itself a fan-out parent has its grandchildren cancelled by the descendant walk running inside the recursive resolution frame BEFORE the sibling's own delete fires (so the parent-handle foreign key, which nulls on delete, doesn't orphan the grandchildren).
- Each sibling row is row-locked (a locking select) before the recursive cancellation fires on it. The lock is held for the duration of the recursive call. This closes the race against a parallel worker on the same supervisor that may be terminating the sibling natively (Commit/Abandon via the executor path) — producer-side claim-id idempotency cannot reconcile distinct verbs (Commit vs Abandon).
- Force-cancelled rows are written to the lineage ledger with a force-cancelled outcome and a cause field distinguishing sibling-cancel from descendant-cancel — see `concept:lineage`.
- The cancel walker SKIPS three classes of sibling rows: (a) non-active rows — committed-durable rows preserve the `concept:claim-lifetime` durable-Commit contract; committed-subgraph and abandoned rows aren't candidates for re-cancellation either; (b) rows already Promoted by an inner recursive walker (a defensive re-check after the row-lock); (c) **rows held by a different supervisor**, per `@blessed-invariant 4` (claimant-guarded release).
- The parent claim-handle row is also gated on its state being active — symmetric with the other auto-terminal paths. Non-active parents have already resolved.
- A malformed aggregation-policy value on the parent → log a warn and treat as no-cancel (safe fallback; the post-resolution aggregator's default-strict path still computes a correct aggregate verdict).

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

## Notes

Introduced by spec:2026-05-15-data-platform-extensions-design §Error policy. The recursive-descent variant (descendants of force-Abandoned siblings get force-Abandoned too) landed in a later cleanup cycle after the single-level implementation was reviewed as spec-violating. The multi-supervisor scope is a documented intentional limitation; if cross-supervisor cancellation is ever needed, options are (a) a DB-mediated "please abandon" signal that the other supervisor's terminal handler reads on tick, or (b) producer-side multi-supervisor coordination on the claim id. Neither is implemented.

State-column refactor per spec:2026-05-17-post-data-platform-cleanup: the skip filter changed from a dedicated held-durable flag to "row is not active." The post-refactor filter is strictly broader (also skips committed-subgraph and abandoned rows) but the behavior is identical because (a) only active rows are cancellation candidates in the first place; (b) the pre-refactor cancel path went through a delete which would have no-op'd on already-deleted committed-subgraph or abandoned rows.

- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
