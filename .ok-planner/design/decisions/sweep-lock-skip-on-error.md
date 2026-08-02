---
decision: sweep-lock-skip-on-error
---

# A scheduler-tick lock error skips the sweep pass

## Choice

In the scheduler tick, an error from the advisory-lock attempt is treated as lock-held: log and skip the sweep pass, never run unlocked (see `concept:advisory-lock`).

## Rationale

The sweeps are periodic recovery; a one-interval delay is benign, while running unlocked under database flakiness allows the concurrent sweeping the lock exists to prevent.

## Alternatives

Prove all sweeps concurrent-safe and run anyway (rejected: a standing proof obligation on a hot extension point).
