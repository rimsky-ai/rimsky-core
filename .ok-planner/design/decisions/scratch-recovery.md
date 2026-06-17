---
decision: scratch-recovery
status: as-is
aliases: []
---

# Per-row scratch survives stale-recovery

## Choice

Per-dispatch row. Every enqueue path that creates a new dispatch row carrying a prior-dispatch reference (the prior-dispatch dispositions are stale-recovery, retry-after-error, and recalculate; the no-prior-dispatch case is excluded) copies scratch from the prior dispatch's row to the new row at row creation. The next dispatch reads scratch from its (new) row via the normal execute-request hydration path. The four call sites are the supervisor's stale-recovery sweep, the cascade-driven recalculate enqueue, the on-error retry enqueue, and the error-policy resolved-action enqueue. The supervisor no longer detects stale via heartbeat-loss; the sweep keys on `last_progress_at` quiet-period (async dispatches) or RPC connection state (sync dispatches) instead.

## Rationale

`story:opaque-executor-scratch`'s acceptance pins the round-trip across any re-dispatch that preserves the prior-dispatch lineage. The enqueue path is the natural copy point since it already creates the new row and stamps the prior-dispatch reference. No new sweep, no new column linkage beyond the prior-dispatch reference (which already exists). Covers retry-after-error so a transient failure can recover its in-flight state.
