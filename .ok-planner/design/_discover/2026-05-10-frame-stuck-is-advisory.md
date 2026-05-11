---
topic: frame-stuck-is-advisory
kind: choice
---

# `frame_timeout_ms` / `last_progress_at` is purely advisory; no destructive action on stuck frames

## Description

A frame represents one cascade resolution (`docs/concepts/frame.md`). A frame that stops progressing (no node-state transitions over a long window) might be wedged (deadlock between handlers / claims, missing webhook, never-arriving review) or might be in a legitimately long stage (a parked node waiting for a deadline). A "kill it after N minutes" policy catches wedged frames at the cost of incorrectly killing legitimately-long ones — and orphans whatever executor work is mid-flight.

Rimsky chose the non-destructive path. `rimsky_frames.last_progress_at` (migration `004-last-outcome-and-progress.sql`) is refreshed on every node-state transition write (`foundation/integration/runner_terminal_handlers.go`). The scheduler tick compares `last_progress_at` against `frame_timeout_ms` and emits a single `frame.stuck.observed` slog warning when the window is exceeded. **No destructive action is taken**: the frame stays `running`, no nodes are failed, the instance is not terminated.

`frame_timeout_ms` is per-template (default 600_000 = 10 minutes; hard floor 60_000 = 60 seconds per `docs/concepts/frame.md`). The CLAUDE.md "Non-obvious gotchas" entry is explicit: "frame_timeout_ms measures 'no progress in window,' not frame age — and is purely advisory. The scheduler tick emits a single `frame.stuck.observed` slog warning when `last_progress_at` ... falls outside the timeout, then takes no destructive action."

A progressing self-invalidate loop refreshes `last_progress_at` every iteration and never trips the warning. A wedged frame whose nodes stop transitioning trips the warning once per tick window and is silent otherwise (the warning is emitted at observation, not on a recurring cron, so operator log volume is bounded by tick frequency).

This is distinct from the per-run executor silence-timeout (which lives on the executor peer): `frame_timeout_ms` is the scheduler-level "is the frame making progress" observation; per-executor silence is the executor-side "did this run go silent" metric. Neither auto-fails a frame.

The pre-v1 design choice is documented in `docs/concepts/frame.md`: "no blanket 'frame too old; kill it' policy. We will revisit destructive timeout behavior, if any, post-v1." Operators are expected to investigate via the dashboard, event log, and `/admin/diagnostics/held-frames`; if they decide the frame is wedged, they can issue admin invalidates or manually mark nodes failed.

The decision sits next to two other "observe-only" surfaces:

- The `retry_loop_no_progress` policy (`docs/concepts/error-policy.md` — `scheduler.max_retries_without_progress`) is the equivalent at the per-dispatch granularity: counts retries without `last_outcome` change and emits an error class when exceeded. Unlike the frame-stuck warning, this one is consumable by error policy and can result in a `failed` node — but at the node, not the frame, level.
- `held-frames` diagnostic endpoint (`docs/concepts/operational-health.md`) surfaces frames with parked nodes; persistently held frames may indicate stuck reviews. Same observe-only philosophy.

## Code surface

- `foundation/persistence/postgres/migrations/004-last-outcome-and-progress.sql` — adds `last_progress_at` + `frame_timeout_ms`.
- `foundation/persistence/frames.go` — read sites for the stuck check.
- `modeling/scheduler/scheduler.go` — tick that emits `frame.stuck.observed`.
- `foundation/integration/runner_terminal_handlers.go` — `last_progress_at` refresh sites.

## Prose surface

- `docs/concepts/frame.md` — "Frame timeout: frame_timeout_ms" section.
- `CLAUDE.md` "Non-obvious gotchas" — explicit non-destructive note.
- `docs/concepts/operational-health.md` — held-frames diagnostic.
- `docs/concepts/error-policy.md` — `max_retries_without_progress` (the per-dispatch sibling).

## Adjacent topics

- `2026-05-10-frame-resolution-model` — the underlying `rimsky_frames` schema.
- `2026-05-10-cascade-fires-on-last-outcome` — `last_outcome` and `last_progress_at` are co-refreshed.
- `2026-05-10-parked-state-and-resume` — parked frames are "held" but not "stuck" if progressing.
- `error-policy-retry-loop-cap` — sibling observe-only mechanism at dispatch level.

## Observations

- "Wedged-but-not-stuck" is a documented blind spot: a heartbeat refresh that updates `last_progress_at` without actual graph progress (e.g. a parked node whose periodic wake re-parks itself) won't trip the warning. CLAUDE.md "Non-obvious gotchas" calls this out: "Acceptable pre-v1."
- The single-warning-per-tick-window cadence is bounded by slog handler config but could be noisy in a deployment with many instances. Operators expecting a one-shot signal might be surprised by repeating warnings.
- A future kill-policy is described as "additive" — it wouldn't change `last_progress_at` semantics. The current frame_stuck warning is therefore future-compatible with a stricter policy.
- The frame-stuck signal is per-tick; there is no separate metric counter at present. Adding a Prometheus counter would be a small change but is not yet in `modeling/observability/metrics.go`.
