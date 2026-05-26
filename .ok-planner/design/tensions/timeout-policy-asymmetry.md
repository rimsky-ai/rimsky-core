---
tension: timeout-policy-asymmetry
category: inconsistent
status: open
affects:
  - frame
  - parked-state
---

# Frame-timeout is purely advisory; park-timeout is destructive — sibling timeout disciplines with opposite policies

## What is muddy

Two timeouts in the system look similar but behave oppositely:

- **`frame_timeout_ms`** (frame stuck warning): the scheduler emits a single `frame.stuck.observed` slog warning when `last_progress_at` falls outside the window. **No destructive action**. The frame stays `running`; nothing is failed.
- **`max_park_duration`** (parked watchdog): when `parked_at + max_park_duration < now()`, the watchdog forces `parked → failed` with `error_class: "park_timeout"`. **Destructive**.

Both are operator-facing safety caps measured against "elapsed time"; one observes, one kills. Documented separately and consistently in each home, but the policy asymmetry isn't surfaced anywhere as a deliberate choice.

## Why it matters

An operator who learns "rimsky doesn't kill on timeout" from the frame surface is surprised when a parked node fails on park-timeout. A future "kill stuck frame" policy proposal needs to know that the asymmetry was deliberate (one observes lack of progress; the other observes time-while-paused).

## Resolution candidates (do NOT pick)

- Surface the deliberate asymmetry — frame-timeout observes, park-timeout kills — in a single "timeout philosophy" statement spanning the frame and parked-state concept definitions, so an operator does not generalize one timeout's behavior to the other (see `concept:frame`, `concept:parked-state`).
- Unify both timeouts as advisory by default, with an optional destructive mode, so the two disciplines share one policy shape.
- Unify both timeouts as destructive by default, with explicit per-template overrides, so the two disciplines share one policy shape.

## Evidence

- `_discover/2026-05-10-frame-stuck-is-advisory.md` Observations bullet 4.
- `_discover/2026-05-10-parked-state-and-resume.md` Observations bullet "no destructive action."
- CLAUDE.md "Non-obvious gotchas" — `frame_timeout_ms`.

