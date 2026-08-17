---
issue: hang-backstop-counts-retry-output-as-progress
kind: human
category: enforcement-gap
artifacts:
  - decision:testing-scenario-based-e2e
status: open
opened: 2026-08-17T19:30:00Z
---

# The hang backstop treats a polling test's retry logging as progress, so a wedged test never dies

`tools/gotest-guard.sh` kills a run only when nothing has started, completed, or emitted output for `RIMSKY_TEST_NO_PROGRESS_SECS`. The project's determinism rules ban a wall-clock deadline in the pass/fail path, so every scenario waiter blocks by polling until its condition holds and logs its observation every fortieth poll. That logging is output. A test wedged forever therefore emits forever, and the guard never fires.

During the sprint that drains the accepted intake, one scenario test wedged on a condition its code could never reach. It ran for nine hours and wrote a 120 MB log of one repeated line. The guard ran beside it and never fired.

The two rules are each right on their own. Blocking on the signal is what makes a correct test pass in milliseconds under any load. Watching for silence is what tells a hung run from a slow one without putting a clock in the verdict. They combine badly because each waiter logs while it polls.

## Options

- Narrow the guard's progress signal to test-level events — a test starting, a test completing, a new subtest appearing — and stop counting a test's own output. A wedged poll then dies at the interval and reports as inconclusive, which is the outcome the guard's own rule names. Cost: the guard's ruled text in `.claude/rules/rules.md` names "emitted output" as a progress signal, so the rule text moves with the code. A long-running test that legitimately runs past the interval without producing a subtest would need its own handling.
- Stop the waiters narrating. Drop the periodic log line from every harness waiter so silence means wedged again. Cost: the log line is the only diagnostic a wedged run leaves, and losing it makes the next wedge harder to read than this one was.
- Leave both as they are and accept that a wedged poll runs until a human notices. Cost: this is the current state, which already consumed nine hours once.

The first option keeps the diagnostic and restores the backstop. The ruling decides whether the guard's progress signal narrows.

## Ruling
