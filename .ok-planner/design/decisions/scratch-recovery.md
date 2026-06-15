---
decision: scratch-recovery
status: as-is
aliases: []
---

# Per-row scratch survives stale-heartbeat recovery

## Choice

Per-dispatch row. Every enqueue path that creates a new dispatch row carrying a prior-dispatch reference (the prior-dispatch dispositions are heartbeat-stale, retry-after-error, and recalculate; the no-prior-dispatch case is excluded) copies scratch from the prior dispatch's row to the new row at row creation. The next dispatch reads scratch from its (new) row via the normal execute-request hydration path. The four call sites are the supervisor's stale-heartbeat sweep, the cascade-driven recalculate enqueue, the on-error retry enqueue, and the error-policy resolved-action enqueue. Same shape as how parked payload flows from a parked row's metadata into the resume context on the park-resume path.

## Rationale

`story:opaque-executor-scratch`'s acceptance pins the round-trip across any re-dispatch that preserves the prior-dispatch lineage. The enqueue path is the natural copy point since it already creates the new row and stamps the prior-dispatch reference. No new sweep, no new column linkage beyond the prior-dispatch reference (which already exists). Covers retry-after-error so a transient failure can recover its in-flight state.

The park-payload field is unchanged by this decision. The new scratch field is independent of the park payload — both exist on the wire and on the node-run persistence side, addressing different surfaces.
