---
decision: timeout-flag
status: adopted
---

# Wall-clock timeout is opt-in

## Choice

The run-to-terminal verbs' wall-clock timeout flag is opt-in with no default; absence means "as long as it takes."

## Rationale

A default wall-clock cap kills legitimate long-running runs. Operators who want a guard add the flag.

## Alternatives

- A default cap the operator can raise or disable — rejected: any default is a load-and-workload guess that eventually kills a legitimate long run; the operator who wants a guard knows their own bound.
