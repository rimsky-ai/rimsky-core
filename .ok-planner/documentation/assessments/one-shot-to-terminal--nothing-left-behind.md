---
assessment: one-shot-to-terminal--nothing-left-behind
subject: story:one-shot-to-terminal
way: nothing-left-behind
release: d977250c
outcome: held
warrant: experiment:one-shot-to-terminal
---
# Nothing to tear down after the invocation returns

The "leaves nothing behind" half is easy to assume rather than check, so the audit settled it by consequence rather than by inspection. The control-API port the run allocated for itself, read out of the run's own transcript, refused connections once the command returned. Nothing therefore remained listening from the stack the invocation had stood up, and the operator has no teardown step to perform — which is what makes the invocation runnable on a machine with no provisioned rimsky infrastructure.

## Unverified remainder

Absence was established through the control-API port the run allocated. The way does not enumerate every resource a run might leave, such as on-disk state, nor does it establish the outcome when the invocation is interrupted rather than allowed to return.
