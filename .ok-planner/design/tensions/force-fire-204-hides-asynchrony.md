---
tension: force-fire-204-hides-asynchrony
category: unclear
status: open
affects:
  - schedule
  - control-api
---

# `POST /admin/scheduled-nodes/{id}/force-fire` returns 204 the moment the row UPDATE commits — the actual invalidate doesn't fire until the next scheduler tick

## What is muddy

`control/controlapi/admin_force_fire.go` exposes the operator escape hatch as a `POST` endpoint that returns `204 No Content` on success. Internally the route calls `foundation/persistence/postgres/schedules.go::ForceFire`, which is `UPDATE rimsky_schedules SET next_fire_at = now() WHERE node_id = $1` — a single SQL statement.

The actual invalidate dispatch is not inline. The next scheduler tick (default `TickInterval = 1500ms`) observes the bumped row through `DueBefore`, computes the new `next_fire_at`, and emits the invalidate at that point. Under multi-replica scheduler the advisory-lock guard ensures exactly-once per slot, but the latency between the 204 response and the actual fire is bounded below by the tick interval, not by the request-response cycle.

A reader / operator seeing 204 reasonably interprets "fire confirmed". The endpoint's return code semantically asserts "row write confirmed" — but the audit-trail event (`schedule_fired`) and the invalidate downstream effect happen up to one tick later.

## Why it matters

The 204 vs the actual asynchronous fire is the kind of distinction that bites in two cases:

1. Test fixtures that issue `force-fire` and then immediately check for a fired-event without waiting (the smoke fixture's 100 sequential force-fires depends on `TickInterval × N` of total latency).
2. Operators triggering a fire and then checking dashboards before the tick observes the row.

The design choice (row-write rather than direct invalidate dispatch) is correct — it preserves the advisory-lock guard and the per-tick orchestration shape that prevents double-fire under multi-replica scheduler. The friction is purely in the API surface: the 204 doesn't convey "queued for next tick" the way a 202 Accepted would.

## Resolution candidates (do NOT pick)

- Return 202 Accepted (with a `Retry-After` or polling hint) instead of 204.
- Document the asynchrony inline in `control/controlapi/admin_force_fire.go` and at `docs/concepts/operational-health.md`.
- Synchronously wait for the next tick before returning (rejected by design — would block the request thread on tick latency).

## Evidence

- `_discover/2026-05-10-cron-no-backfill.md` Observations bullet "force-fire is row-write, not invalidate-dispatch".
- `control/controlapi/admin_force_fire.go`.
- `foundation/persistence/postgres/schedules.go`.

