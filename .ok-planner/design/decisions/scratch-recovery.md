---
decision: scratch-recovery
status: as-is
aliases: []
---

# Per-row scratch survives stale-recovery

## Choice

Per-dispatch row. Every enqueue path that creates a new dispatch row carrying a prior-dispatch reference (the prior-dispatch dispositions are stale-recovery and recalculate; the no-prior-dispatch case is excluded) copies scratch from the prior dispatch's row to the new row at row creation. The next dispatch reads scratch from its (new) row via the normal execute-request hydration path. The call sites are the supervisor's stale-recovery sweep and the cascade-driven recalculate enqueue. The supervisor no longer detects stale via heartbeat-loss; the sweep keys on `last_progress_at` quiet-period (async dispatches) or RPC connection state (sync dispatches) instead. Policy retry does not enter this path: under `decision:in-place-retry`, retries loop in-process on the same dispatch row, so the scratch persisted on that row is already available to the retry attempt without any copy.

## Rationale

`story:opaque-executor-scratch`'s acceptance pins the round-trip across any re-dispatch that preserves the prior-dispatch lineage. The enqueue path is the natural copy point since it already creates the new row and stamps the prior-dispatch reference. No new sweep, no new column linkage beyond the prior-dispatch reference (which already exists). Stale-recovery is the load-bearing case: a supervisor crash mid-dispatch leaves the row stale; the recovery sweep re-enqueues the work and the new row needs the in-flight scratch the prior row had accumulated.
