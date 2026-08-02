---
decision: termination
---

# Run-to-terminal verbs exit at instance-terminal

## Choice

The run-to-terminal verbs wait for every declared instance to reach instance-terminal state per `concept:terminal-resolution`, then exit.

## Rationale

Park is handled by the supervisor's existing policy (the time-wake at the park's required resume-at), so parked nodes do not require special verb-level handling. The supervisor's existing instance-terminal promotion path is the natural completion gate.

## Alternatives

- Exit once instances are created and their roots dispatched — rejected: the verbs' promise is a terminal outcome an operator can script against, not submission.
- Verb-level park handling (treating a park as done, or expiring it from the verb) — rejected: duplicates the supervisor's park policy in a second place.
