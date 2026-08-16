---
experiment: verifier-severity-partition
commit: d977250c
---

# Warning-severity and error-severity checks in the bundled verifier

## What it ran against

A `rimsky-all-in-one` container booted from this tree's image at
`RIMSKY_IMAGE_TAG`, on its zero-config SQLite defaults, driving the in-process
`verifier-shape-checks` executor. `run.py` registers three templates that differ
only in the `severity` labels on their declared checks, drives one instance of
each to rest, and reads each verifier node back off the node observability
route. It boots and removes its own container.

## What was observed

Three legs, eight checks, none failing.

A failing `no_nulls` check labelled `warning`, beside a passing
`numeric_range` check labelled `error`, settled the node fresh with no failed
run. The node's attributes recorded `verifier_warning_count: 1` and a
`verifier_warnings` entry naming the kind `no_nulls` and the severity
`warning`.

Relabelling that same check `error`, over the same rows, flipped the outcome:
the node settled with one failed run, no fresh run, and the terminal signal
`terminal/error/verifier/check_failed/no_nulls`. The severity label is
therefore what decides, since nothing else changed.

Rows tripping both checks blocked on the error-severity one: the terminal
error class was `verifier/check_failed/numeric_range`, and its payload carried
the `numeric_range` failure under `failures` and the `no_nulls` failure under
`warnings` in the same record.
