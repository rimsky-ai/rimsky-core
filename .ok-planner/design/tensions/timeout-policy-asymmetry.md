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

- Document the asymmetry in a single "timeout philosophy" section in `docs/concepts/operational-health.md`.
- Unify both as advisory + an optional destructive-mode flag.
- Unify both as destructive with explicit overrides per template.

## Evidence

- `_discover/2026-05-10-frame-stuck-is-advisory.md` Observations bullet 4.
- `_discover/2026-05-10-parked-state-and-resume.md` Observations bullet "no destructive action."
- CLAUDE.md "Non-obvious gotchas" — `frame_timeout_ms`.

