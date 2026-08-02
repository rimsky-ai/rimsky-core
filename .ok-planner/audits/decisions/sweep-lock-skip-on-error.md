---
audit: sweep-lock-skip-on-error
artifact: decision:sweep-lock-skip-on-error
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:28:34Z
---

# A scheduler-tick advisory-lock error skips the whole sweep pass

Supported. `scheduler.go`'s `tick` function, at the `@decision: sweep-lock-skip-on-error`-annotated site, checks the error return from `AdvisoryLocker.TrySchedulerTick`: on error it logs a warning and returns immediately (`return nil`), running none of the subsequent sweeps (pure-cascade processing, executor-deadline sweep, orphaned-claim-handle sweep, retention sweeps) — never falling through to run unlocked. A direct test, `TestScheduler_AdvisoryLockErrorSkipsSweepPass`, exercises this branch.
