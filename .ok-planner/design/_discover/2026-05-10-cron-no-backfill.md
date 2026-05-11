---
topic: cron-no-backfill
kind: choice
---

# Schedule cron advances from `row.NextFireAt`; missed fires are not backfilled

## Description

A schedule is a node-level cron expression (`docs/concepts/operational-health.md` "Watchdog graphs" — `cron:` on a node fires it at the configured cadence). Schedules drive scheduled invalidates: when the schedule's `next_fire_at` reaches now, the scheduler tick fires an invalidate against the scheduled node and advances `next_fire_at` by one cron slot.

**Tick → fire pipeline.** `modeling/scheduler/scheduler.go::tick` (lines 216-353) runs ten ordered phases per tick; `ProcessSchedules` is phase 2 (lines 236-247). The tick interval defaults to 1500ms (`scheduler.go:145-147`). The whole tick is gated by an advisory lock (`TrySchedulerTick`) so multiple scheduler replicas don't double-fire.

`ProcessSchedules` (`modeling/scheduler/schedule_ticker.go:64-131`) implements the per-tick body:

1. `Schedules().DueBefore(ctx, clock.Now(), tx)` — read all rows with `next_fire_at <= clock.Now()` (line 67). Single short read tx.
2. For each due row:
   a. `lookupInstanceForNode` (line 78; helper at lines 136-147) reads the node's instance_id for event-log attribution.
   b. `NextFireAt(row.CronExpr, row.NextFireAt)` (line 82) computes the new fire time **strictly after the row's previous `next_fire_at`**, NOT after `clock.Now()`. Uses `robfig/cron/v3` `ParseStandard` (`schedule_ticker.go:44-50`) — 5-field cron in UTC. Parse failures emit a `schedule_dispatch_failed` event and the row is skipped.
   c. `Schedules().RecordFired(ctx, nodeID, next, firedAt, tx)` (line 95) writes the new `next_fire_at` + `last_fired_at` in its own short tx.
   d. `disp.EmitInvalidate(...)` (lines 104-114) routes via `scheduleDispatcherAdapter` (`scheduler.go:387-398`) → `integration.InvalidateNode`, which triggers `frame.EnqueueOrCoalesce` per the template's `frame_resolution`. The invalidate reason is the literal string `"schedule_fired"`.
   e. `schedule_fired` event appended (lines 116-127).

Per-row error containment: each step's tx is independent, and any failure logs a `schedule_dispatch_failed` event and continues to the next row (lines 86-114). One bad cron expression doesn't block neighbors.

**Missed-fire policy: no backfill.** The advancement is computed from the row's prior `next_fire_at`, not from `clock.Now()`. After a multi-slot outage, the scheduler produces one fire on recovery and advances by one slot — it does not enumerate the missed slots.

Stated rationale at `modeling/scheduler/schedule_ticker.go:6-14`: "backfilling a 6-hour outage for an hourly schedule would generate a thundering herd of six identical-payload runs with only their timestamps differing; the intent of cron-backed invalidation is freshness, which a single post-outage fire satisfies."

**Interaction with frames.** The per-row `EmitInvalidate` call eventually reaches `frame.EnqueueOrCoalesce` (`modeling/frame/producer.go:26-43`). The instance's template `frame_resolution` mode controls whether the schedule fire creates a fresh frame (`serial_queue`) or joins/creates the pending coalesce row (`coalesce`). A schedule that fires three times before its first frame completes will: under `serial_queue`, produce three queued frames running in arrival order; under `coalesce`, produce one queued frame whose `source_node_ids[]` contains the schedule's node (appended once due to the `array_append` guard at `foundation/persistence/postgres/frames.go:312-315`). The "no backfill" property and the "coalesce drops duplicates" property compose: post-outage, you get exactly one fire that reaches exactly one queued coalesce row.

**Force-fire admin endpoint.** `POST /admin/scheduled-nodes/{node_id}/force-fire` (`modeling/controlapi/admin_force_fire.go:42-62`) bumps `rimsky_schedules.next_fire_at = now()` in a single SQL statement (`foundation/persistence/postgres/schedules.go:97-106`). The endpoint returns 204 immediately and does not wait for the scheduler tick to pick up the row. The next tick (within `TickInterval`, default 1500ms) picks up the bumped row via the same `DueBefore` predicate. Operator semantics: the endpoint doesn't fire the invalidate directly — it just makes the schedule "due" — so the normal scheduler-side ordering + advisory-lock guarantees apply (no double-fire under multi-replica scheduler). The endpoint is documented as admin-only but currently relies on the global `AppDeps.Auth` middleware; without an Authenticator configured the route is anonymous (`admin_force_fire.go:24-27`).

The cron expression is a robfig/cron/v3 standard 5-field expression (`schedule_ticker.go:44-50`).

The advancement-from-row-NextFireAt rule is `@blessed-invariant`-adjacent: it's not in CLAUDE.md's blessed invariants list, but it's documented inline as a deliberate design choice that operators can rely on. The compose between "single post-outage fire" and "consistent on-rhythm cadence after the fire" is precisely what the row-based advancement delivers.

## Code surface

- `modeling/scheduler/schedule_ticker.go:64-131` — `ProcessSchedules`: the per-tick body (167-line file total).
- `modeling/scheduler/schedule_ticker.go:44-50` — `NextFireAt` (robfig/cron/v3 `ParseStandard`).
- `modeling/scheduler/scheduler.go:216-353` — `tick`: ten-phase per-tick orchestration; phase 2 is `ProcessSchedules`.
- `modeling/scheduler/scheduler.go:387-398` — `scheduleDispatcherAdapter.EmitInvalidate` → `integration.InvalidateNode`.
- `modeling/scheduler/pure_cascade.go` — pure-cascade sweep (phase 3, sibling of schedule firing).
- `foundation/persistence/schedules.go` — Go-side CRUD on `rimsky_schedules` (interface + types).
- `foundation/persistence/postgres/schedules.go:50-65` — `Register` (upsert next_fire_at).
- `foundation/persistence/postgres/schedules.go:67-79` — `DueBefore` (the SELECT predicate).
- `foundation/persistence/postgres/schedules.go:82-93` — `RecordFired` (writeback of next_fire_at + last_fired_at).
- `foundation/persistence/postgres/schedules.go:94-106` — `ForceFire` (admin escape hatch: `SET next_fire_at = now()`).
- `foundation/persistence/postgres/migrations/001-initial.sql` — `rimsky_schedules` schema.
- `modeling/controlapi/admin_force_fire.go:42-62` — `POST /admin/scheduled-nodes/{node_id}/force-fire` route.
- `modeling/controlapi/admin_routes_test.go` + `app_test.go::TestAdminForceFire_RouteWired` — route wiring check.
- `modeling/frame/producer.go:26-43` — `frame.EnqueueOrCoalesce` (schedule fire → frame).

## Prose surface

- `CLAUDE.md` "Non-obvious gotchas" — "Schedule cron advances from row.NextFireAt, not clock.Now()."
- `docs/concepts/operational-health.md` — `cron:` and `force-fire`.
- `docs/concepts/frame.md` — "Frames are never backfilled: a missed schedule fire does not retroactively create a frame; the schedule advances from the recorded next-fire-at, not from the wall clock."

## Adjacent topics

- `2026-05-10-frame-resolution-model` — frames are the unit of cascade; schedules drive frame creation.
- `2026-05-10-stdlib-slog-and-minimal-deps` — robfig/cron/v3 is one of the few approved third-party libraries.

## Observations

- The phrasing in `docs/concepts/frame.md` ("Frames are never backfilled") and the schedule_ticker comment line up; both lean on the same "freshness is the goal, not historical completeness" argument.
- A schedule that's never fired before is gated by its persisted `next_fire_at` only; rimsky does not infer "should have fired earlier" from the cron expression's start time. The first-time initialization of `next_fire_at` happens at schedule registration (in `controlapi`); a schedule registered with `next_fire_at` already in the past will fire on the next tick.
- `force-fire` admin endpoint is described as bypassing cron (CLAUDE.md cites it in the smoke fixture as the driver of 100 sequential fires). Its existence acknowledges that the cron-no-backfill rule is normal-operations behavior; deterministic test runs use the admin escape hatch.
- The cron expression itself is opaque to rimsky beyond robfig/cron/v3's parsing; the library version is pinned (`stdlib + minimal deps`). A `cron` syntax extension (year field, seconds field) would require a library bump.
- **Tension candidate (force-fire is row-write, not invalidate-dispatch):** the admin endpoint returns 204 the moment the `next_fire_at` UPDATE commits. The actual invalidate doesn't happen until the *next* scheduler tick observes the row in `DueBefore`. Under default 1500ms tick interval that's at most ~1.5s of latency, but operators reading the endpoint's response as "fire confirmed" are reading a row-write confirmation. The alternative — direct in-handler invalidate dispatch — would bypass the advisory-lock guard and the per-tick orchestration; the row-write design is correct, but the endpoint's response code (204 No Content) makes the asynchrony invisible.
- **Tension candidate (schedule_fired reason is a string literal in two files):** the invalidate `Reason` field is the hardcoded string `"schedule_fired"` at `schedule_ticker.go:107` while the event log uses `Kind: "schedule_fired"` at line 120. Two separate string literals, not a shared constant; an audit-trail reader has to know they refer to the same thing.
- **Tension candidate (no observability of coalesced fires):** a schedule firing N times before its first frame completes, under `coalesce`, deduplicates the source node — but the supervisor-side audit log records N `schedule_fired` events. There's no event saying "this schedule's invalidate was coalesced into an existing pending frame." An operator tracing why a schedule fired but the dependent recompute didn't run won't find a clear marker.
- **Tension candidate (per-row tx vs tick-wide advisory lock):** the per-row failure containment (each row in its own short tx) lives inside the broader tick's advisory-lock-held window. A long DB connection that the row's tx is using doesn't block other replicas from running their own tick (because they're skipped by `TrySchedulerTick`), but it does mean a single slow row can hold the lock for the whole loop duration. The current 5-tick orphan-claim cutoff and 1500ms tick interval gives ~7.5s of headroom — fine in practice, but the math depends on staying within `TickInterval × small N`.
