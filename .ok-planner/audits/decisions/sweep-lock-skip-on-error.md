---
audit: sweep-lock-skip-on-error
artifact: decision:sweep-lock-skip-on-error
text: compliant
implementation: supported
commit: d977250c
audited: 2026-08-16T05:38:09Z
---

# A scheduler-tick advisory-lock error skips the sweep pass rather than running unlocked

Supported. The tick attempts the advisory lock first; an error from the attempt logs a warning naming the failure and returns immediately, exactly as the not-held branch does, so the pass ends before any sweep runs. Every sweep in the tick — pure cascade, executor deadlines, orphaned claim handles, the four retention sweeps, parked nodes, and the breakpoint sweeps — sits after that guard and behind the lock's deferred release, so no sweep can run on a failed lock attempt. A unit test installs a locker that always errors, runs one tick, and asserts both that the lock was attempted exactly once and that a stale run row was left untouched and unclaimed.
