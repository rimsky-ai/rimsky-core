---
experiment: template-error-policy
commit: PENDING
---

# Per-error-class routing actions honoured at the error site

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image. Four templates
declare the same node against the same deterministic executor failure — a
shape check requiring at least one row against no rows, which always errors
with the class `verifier/check_failed/row_count_absolute` — and differ only in
the routing action declared for that class. `run.sh` boots and removes the
container.

## What was observed

Ten checks, none failing. All four actions of the declared vocabulary were
honoured. Under `pass` the run settled fresh while its settling signal still
named the error class. Under `give_up` the run settled failed. Under `retry`
with a declared cap of two, the runtime emitted `transient/retry/1/...` and
`transient/retry/2/...`, took no third retry, and settled the run failed once
the budget was spent. Under `release_and_requeue` each failure emitted
`transient/release_and_requeue/<class>` and the run was dispatched again, never
settling fresh or failed — it went back for another attempt, which is what the
action names.
