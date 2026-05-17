---
concept: parked-state
status: as-is
aliases:
  - park
  - parked node
references:
  - _discover/2026-05-10-parked-state-and-resume.md
  - _discover/2026-05-10-state-machine-no-self-loop.md
  - _discover/2026-05-10-orphan-reaper-no-producer-abandon.md
---

# Parked state

## What it is

`parked` is the fifth legal `node-state` value, entered from `running` when the executor emits `ParkRequested`. While parked, the node is not running and not failed; it carries a `parked_payload`, optional `session_token`, optional `resume_at`, and `parked_reason`. The corresponding `rimsky_node_runs.phase` is `'parked'`.

## Purpose

Some workloads (human review, scheduled wake, external event wait) cannot finish in a bounded window. `parked` gives them a first-class hold state with explicit resume semantics, instead of forcing them through `failed`+retry (which loses session context) or keeping a gRPC stream open indefinitely.

## Boundaries

Owns: the hold-state schema (park columns on `table:rimsky_node_runs`), the three exit paths (time-wake, external invalidate, watchdog timeout), the `ResumeContext` passed back on re-dispatch. Does NOT own: held-claim resolution (that's `auto-terminal`); orphan reaping (parked rows are explicitly skipped). Adjacent: `node-state`, `node-run`, `auto-terminal`, `claim-handle` (including its `### Held variant` subsection), `blob-backend` (parked_payload spills via the same mechanism).

## Invariants

- Cascade does not propagate from `parked` (CLAUDE.md "Held vs failed states").
- The orphan-claim reaper skips `phase='parked'` rows because parked nodes do not heartbeat (`@blessed-invariant 6` exception).
- Time-wake and external-invalidate both transition `parked → stale` (never directly to `running`); the next supervisor tick re-dispatches. Watchdog timeout is the one destructive exit (`parked → failed` with `error_class: "park_timeout"`).
- Held-claim auto-terminal continues to fire correctly across park because `rimsky_claim_holders.state` stays `'active'` while the node is parked.

## Aliases and historical names

The state was added under the platform-extensions design (2026-05-08); migration 006 extends the `phase` CHECK constraint to include `'parked'`.

## Open within this concept

- "No destructive action" (frame-stuck) vs "destructive watchdog timeout" (parked) are sibling timeout disciplines with opposite policies — see `tensions/timeout-policy-asymmetry.md`.


## Notes

- 2026-05-14: `parked_reason` is now typed (proto enum `ParkReason`); the column stores the snake_case form (`time_wait` / `signal_wait` / `awaiting_human` / `retry_backoff`). New `parked_reason_note` column carries the free-form human annotation. The diagnostics endpoint `?reason=` filter validates against the enum. See spec Piece 2 `.ok-planner/specs/2026-05-14-subscription-cascade-and-quality-of-life-design.md`.
- 2026-05-15: **4-reason taxonomy + freeform label**. The proto enum is `PARK_REASON_UNSPECIFIED | PARK_REASON_TIME_WAIT | PARK_REASON_CALLBACK_WAIT | PARK_REASON_RETRY_BACKOFF | PARK_REASON_OTHER`. The column stores the storage form (`time_wait` / `callback_wait` / `retry_backoff` / `other`); `parked_reason_label` carries the freeform label (required when `parked_reason = other`). The watchdog consults a per-reason `max_park_duration` config (`time_wait: 1h`, `callback_wait: 7d`, `retry_backoff: 1h`, `other: 1h` defaults); timeout produces `failed{error_class: "park_timeout"}`. Bundled emitter updates: long-running-job executors emit `CALLBACK_WAIT`, time-based polling executors emit `TIME_WAIT`, rate-limit-aware executors emit `RETRY_BACKOFF`. See `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md` §Parked-state taxonomy.
