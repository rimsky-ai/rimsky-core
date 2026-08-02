---
decision: scratch-recovery
---

# Per-row scratch survives stale-recovery

## Choice

Scratch is a property of the dispatch row. The one enqueue path that creates a new row carrying a prior-dispatch reference — the cascade-driven recalculate enqueue — copies scratch from the prior run's row at row creation, and the next dispatch hydrates from its new row normally. Dead-supervisor recovery creates no new row: the sweep releases the orphaned claim on the same row, which re-enters the pending-queue claim path, so its already-persisted scratch needs no copy. Policy retry loops in-process on the same row (per `decision:in-place-retry`), carrying the failed attempt's terminal scratch directly into the next acquisition.

## Rationale

`story:opaque-executor-scratch` pins the round-trip across any re-dispatch that preserves the prior-dispatch lineage. The recalculate enqueue is the natural copy point since it already creates the new row and stamps the prior-dispatch reference — no new sweep, no new column linkage. Recovery reusing the stale row is what keeps its accumulated scratch intact for the next claimant.

## Alternatives

- A node-scoped scratch store independent of dispatch rows — rejected: needs its own table, sweep, and lifecycle, and divorces scratch from the dispatch lineage the story pins the round-trip to.
