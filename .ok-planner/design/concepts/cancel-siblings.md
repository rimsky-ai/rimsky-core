---
concept: cancel-siblings
---

# Cancel siblings

## What it is

The proactive sibling cancellation that the strict aggregation policy always performs: when one sub-claim resolves to Abandon — whether the child's own natural abandon or a forced cancel — under a parent whose policy is strict, the runtime unconditionally walks the parent's other in-flight sub-claims and force-Abandons each via recursive claim-handle terminal-resolution calls. This is what "fail fast" means for strict aggregation; it is not a separate, configurable behavior — it is intrinsic to choosing strict. A workflow that wants surviving siblings to keep running despite one failure chooses a different aggregation policy kind instead (see `concept:fan-out`); a threshold policy set to the full child count tolerates any number of failures short of total loss without ever triggering this walk.

Snapshotted on the parent claim-handle row at acquire-time from the fan-out parent's aggregation policy. Implemented as a sibling-level cancel walk plus a recursive descendant walk (see `concept:claim-tree`).

## Boundaries

Owns: the proactive cancel walker, the multi-supervisor scope filter. Does NOT own: the post-resolution aggregate verdict (see `concept:fan-out` aggregator), the `strict` policy's semantics itself (owned by `concept:fan-out`; `concept:node-run` only snapshots the value on the parent run for run-tree aggregation), the held-durable promotion (see `concept:claim-lifetime`), the unconditional recursive descendant-cancel walk that fires on any Abandon regardless of aggregation policy (see `concept:claim-tree`), the run-level force-cancellation of remaining in-flight clones under `strict`/`first` — a separate mechanism, keyed off the run tree rather than the claim tree, that reaches clones with no active claim to walk (see `concept:fan-out`, `concept:node-run`). Adjacent: `concept:claim-tree`, `concept:fan-out`, `concept:claim-co-holdership`, `concept:claim-lifetime`.

## Invariants

- Proactive cancellation fires inside the triggering child's terminal-resolution call, AFTER the producer Abandon verb and the parent-counter bump, BEFORE the parent's recursive resolution walk.
- Each force-Abandoned sibling is force-Abandoned via the same terminal-resolution path; the recursion runs the standard counter-bump + lineage-write + cancel-descendants chain, so the parent's aggregate verdict ends up consistent regardless of how many siblings were force-cancelled.
- The recursion is bounded by claim-tree depth, not configuration. A force-Abandoned sibling that is itself a fan-out parent has its own descendants cancelled by the unconditional descendant-cancel walk (see `concept:claim-tree`) running inside the recursive resolution frame BEFORE the sibling's own terminal resolution — promotion to abandoned, or deletion under the ownership-bail source — so the descendants are not left orphaned in-flight.
- Each sibling row is row-locked (a locking select) before the recursive cancellation fires on it. The lock is held for the duration of the recursive call. This closes the race against a parallel worker on the same supervisor that may be terminating the sibling natively (Commit/Abandon via the executor path) — producer-side claim-id idempotency cannot reconcile distinct verbs (Commit vs Abandon).
- Force-cancelled rows are written to the lineage ledger with a force-cancelled outcome and a cause field distinguishing sibling-cancel from descendant-cancel — see `concept:lineage`.
- The cancel walker SKIPS three classes of sibling rows: (a) non-active rows — committed-durable rows preserve the `concept:claim-lifetime` durable-Commit contract; committed-subgraph and abandoned rows aren't candidates for re-cancellation either; (b) rows already Promoted by an inner recursive walker (a defensive re-check after the row-lock); (c) **rows held by a different supervisor**, per invariant 4 (claimant-guarded release).
- The parent claim-handle row is also gated on its state being active — symmetric with the other auto-terminal paths. Non-active parents have already resolved.
- A malformed aggregation-policy value on the parent → log a warn and treat as non-strict, i.e. no proactive cancellation (safe fallback; the post-resolution aggregator's default-strict path still computes a correct aggregate verdict).

## Multi-supervisor scope (load-bearing)

**Cancel-siblings is scoped to the supervisor that holds the parent.** Under multi-supervisor deployments (more than one supervisor process running concurrently), sub-claims of the same parent can be acquired by different supervisor processes. The cancel walker filters mismatched-supervisor siblings out of its walk per invariant 4: a supervisor cannot release claims held by a different supervisor.

Practical consequence under a `strict` aggregation policy with multi-supervisor fan-out:

- Supervisor A holds 5 of 12 sub-claims; supervisor B holds the other 7.
- One of A's sub-claims resolves to Abandon.
- A's cancel walker force-Abandons A's other 4 sub-claims.
- A's cancel walker SKIPS B's 7 sub-claims (claimant-guard filter).
- B's 7 sub-claims continue to natural completion; each Commit / Abandon bumps the parent counter independently.
- The parent's aggregator computes the final verdict from the union of A's force-Abandons + B's natural outcomes.

"Fail fast" is honored within a supervisor, not across. The producer side is also protected: forcing an Abandon from supervisor A on a claim held by supervisor B would race with B's natural Commit/Abandon and corrupt the producer's `claim_id`-keyed state.

Fan-out is typically single-supervisor in practice (the supervisor that acquired the parent dispatches the children; single-replica deployments are the common case). The multi-supervisor edge case matters when (a) more than one supervisor replica is deployed, AND (b) multiple supervisors picked up sibling sub-claim rows in parallel, AND (c) the parent's policy is strict.
