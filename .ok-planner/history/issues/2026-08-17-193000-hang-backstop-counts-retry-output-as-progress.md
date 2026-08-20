---
issue: hang-backstop-counts-retry-output-as-progress
kind: human
category: enforcement-gap
artifacts:
  - decision:testing-scenario-based-e2e
status: answered
opened: 2026-08-17T19:30:00Z
---

# The hang backstop treats a polling test's retry logging as progress, so a wedged test never dies

## Question

Every scenario waiter used to log its observation every fortieth poll.
Does `tools/gotest-guard.sh`'s no-progress backstop still treat that
retry logging as progress forever, so a genuinely wedged test emits
forever and the guard never fires?

## Answer

The gap is closed. Every scenario-test wait helper now logs its
"waiting for..." observation once, on the first poll, not periodically.
`test/support/awaited/awaited.go::Until`,
`test/support/eventwait/eventwait.go::WaitForEvent`, and every
`WaitFor*` helper in `test/support/scenario/harness.go` gate their
`t.Logf` call behind `if poll == 1`. A test wedged on a condition it can
never reach emits that one line, then goes silent for the rest of its
polling loop.

`tools/gotestguard/main.go`'s `progress.touch()` still fires on every
scanned output line — the guard's polarity has not changed. But nothing
keeps re-touching progress once a wait is truly stuck, because the
periodic narration is gone. `RIMSKY_TEST_NO_PROGRESS_SECS` now trips
correctly and kills a wedged run.

`decision:testing-scenario-based-e2e` states the intended shape
directly: "Harness wait helpers are poll-until-success: they block
until the awaited state appears, and they report the
expected-versus-observed state descriptively when the run is cut
short... hang detection lives in the test guard, which watches the
runner's event stream and kills a run only when no test has completed
for a long interval."

The sprint that converted every wait to this one-shot-log shape landed
the issue's own second option: "stop the waiters narrating... so
silence means wedged again." It avoided the cost the issue weighed
against that option — the single first-poll line still names the
awaited condition, so the next wedge still leaves one diagnostic line
behind.

Re-verified against the current tree: the filed gap no longer exists.
