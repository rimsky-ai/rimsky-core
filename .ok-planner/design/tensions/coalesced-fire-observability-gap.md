---
tension: coalesced-fire-observability-gap
category: unspecified
status: open
affects:
  - sensor
  - frame
  - event-log
---

# Coalesce-mode invalidates produce a single queued frame, but the supervisor-side event log shows N `schedule_fired` events with no marker indicating which were coalesced

## What is muddy

Under `frame_resolution_mode: coalesce`, a schedule firing N times before its first frame completes deduplicates the source node into a single queued row (`array_append` guard at `foundation/persistence/postgres/frames.go`). The supervisor's audit log faithfully records N `schedule_fired` events (one per row processed by `ProcessSchedules`, emitted at `graph/scheduler/schedule_ticker.go`) — but there is no companion event that says "this fire was coalesced into an existing pending frame" or "this fire created a fresh queued frame".

An operator tracing why a schedule fired 6 times during an outage but the dependent recompute only ran once cannot distinguish the cases from the event log alone:

- 6 fires + 6 fresh frames running serially (`serial_queue` mode), or
- 6 fires + 1 queued frame coalescing 5 of them (`coalesce` mode), or
- 6 fires where the schedule's owning frame was already running and 5 deduped into the queued sibling.

The information is inferable by joining `rimsky_schedules.last_fired_at` against `rimsky_frames.created_at` / `state_changed_at` and counting `source_node_ids[]`, but there's no first-class log entry.

## Why it matters

`coalesce` is the recommended mode for "freshness-only" workloads where N fires reducing to 1 is the desired behavior — but the audit-trail invisibility makes it hard to validate that coalescing actually happened in production. Adjacent to `frame-stuck-is-advisory`: both are cases where "the platform took a non-default path silently". Operators reading the event log to debug a "missed run" have to know enough about the frame producer's internals to compute the right join query.

## Resolution candidates (do NOT pick)

- Make coalescing a first-class audited event: when an incoming fire merges into an existing pending frame rather than creating a fresh one, emit a distinct audit entry so the event log distinguishes coalesced fires from fresh ones (see `concept:frame`, `concept:event-log`).
- Expose a coalesced-fire counter in the observability surface alongside the existing frame-created counter, so operators can validate that coalescing happened without reconstructing it from a join (see `concept:observability`).
- Capture in the frame concept's definition how the coalesce-vs-fresh distinction is currently inferable from the persisted state, so operators have a documented technique until a first-class signal exists.

## Evidence

- `_discover/2026-05-10-cron-no-backfill.md` Observations bullet "no observability of coalesced fires".
- `foundation/persistence/postgres/frames.go` — `EnqueueCoalesceFrame` body.
- `graph/scheduler/schedule_ticker.go` — the `schedule_fired` event emit.

