---
concept: schedule
status: as-is
aliases:
  - scheduled-node
references:
  - _discover/2026-05-10-cron-no-backfill.md
  - _discover/2026-05-10-frame-resolution-model.md
---

# Schedule

## What it is

A node-level cron expression declared in templates. Stored in `rimsky_schedules` with `cron_expr`, `next_fire_at`. The scheduler tick (`modeling/scheduler/scheduler.go::tick`, lines 216-353) runs ten ordered phases; `ProcessSchedules` is phase 2 (`schedule_ticker.go:64-131`). The whole tick is gated by an advisory lock (`TrySchedulerTick`) so multiple scheduler replicas don't double-fire.

**Per-tick body:**

1. `Schedules().DueBefore(ctx, clock.Now())` reads rows with `next_fire_at <= now`.
2. For each due row, in its own short tx:
   a. Look up the node's instance for event attribution.
   b. `NextFireAt(row.CronExpr, row.NextFireAt)` computes the new fire time **strictly after the row's previous `next_fire_at`**, not after `clock.Now()`. Uses `robfig/cron/v3` `ParseStandard` (5-field cron, UTC).
   c. `RecordFired` writes `next_fire_at` + `last_fired_at`.
   d. `disp.EmitInvalidate(...)` routes via `scheduleDispatcherAdapter` (`scheduler.go:387-398`) → `integration.InvalidateNode` → `frame.EnqueueOrCoalesce` per the template's `frame_resolution`. Invalidate `Reason` is the literal string `"schedule_fired"`.
   e. Append a `schedule_fired` event.

A per-row tx failure logs a `schedule_dispatch_failed` event and continues to the next row — one bad cron expression doesn't block neighbors.

## Purpose

Periodic recomputation without an external scheduler. The cron-no-backfill discipline preserves "freshness over completeness": a 6-hour outage on an hourly schedule produces one post-outage fire, not six.

## Boundaries

Owns: cron parsing, `next_fire_at` advancement, the schedule-ticker loop, the `force-fire` admin endpoint. Does NOT own: frame creation (the fire calls `frame.EnqueueOrCoalesce`; see `frame`), cascade walks. Adjacent: `frame`, `invalidate`, `advisory-lock` (the tick gate), `cascade`.

## Invariants

- `next_fire_at` advances from `row.NextFireAt`, NOT `clock.Now()`. Missed fires are NOT backfilled.
- `POST /admin/scheduled-nodes/{id}/force-fire` (`modeling/controlapi/admin_force_fire.go:42-62`) bumps `rimsky_schedules.next_fire_at = now()` in a single SQL statement (`foundation/persistence/postgres/schedules.go:97-106`) and returns 204 immediately — it does not invoke the invalidate inline. The next scheduler tick picks the row up via the same `DueBefore` predicate.
- Frame-mode interaction: a schedule firing N times before its first frame completes produces N queued frames under `serial_queue` (arrival order) but exactly one queued row under `coalesce` (source node appended once via `array_append` guard at `foundation/persistence/postgres/frames.go:312-315`).
- The advisory-lock-held window covers the entire tick (including the per-row tx loop): a slow row blocks subsequent rows on the same tick but does not block other replicas (they're skipped by `TrySchedulerTick`).
- Robfig/cron/v3 5-field expressions; library version pinned.

## Aliases and historical names

None live.

## Open within this concept

- `force-fire` returns 204 the moment the row UPDATE commits; the actual invalidate is asynchronous — see `tensions/force-fire-204-hides-asynchrony.md`.
- No audit-trail marker for invalidates that get coalesced into an existing queued frame — see `tensions/coalesced-fire-observability-gap.md`.

