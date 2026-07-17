---
decision: scratch-recovery
status: as-is
aliases: []
---

# Per-row scratch survives stale-recovery

## Choice

Per-dispatch row. The single enqueue path that creates a new dispatch row carrying a prior-dispatch reference is the cascade-driven recalculate enqueue: it copies scratch from the prior run's row to the new row at row creation, and the next dispatch reads scratch from its (new) row via the normal execute-request hydration path. Dead-supervisor recovery does not create a new row: the sweep releases the orphaned claim on the same row — keyed on `last_progress_at` quiet-period for async dispatches, or RPC connection state for sync dispatches, never heartbeat-loss — and the row re-enters the normal pending-queue claim path for another supervisor to pick up; since it is the same row, its already-persisted scratch needs no copy. Policy retry does not enter either path: under `decision:in-place-retry`, retries loop in-process on the same dispatch row — the failed attempt's terminal scratch is copied in-memory into the retry's next acquisition, with no row read, since it is the same row throughout.

## Rationale

`story:opaque-executor-scratch`'s acceptance pins the round-trip across any re-dispatch that preserves the prior-dispatch lineage. The recalculate enqueue path is the natural copy point since it already creates the new row and stamps the prior-dispatch reference. No new sweep, no new column linkage beyond the prior-dispatch reference (which already exists). Dead-supervisor recovery needs no copy because it never creates a new row — reusing the stale row is what keeps its accumulated scratch intact for the next claimant.
