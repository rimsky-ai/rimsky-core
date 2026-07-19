---
decision: termination
status: adopted
---

# termination

## Choice

The verb waits for every declared instance to reach instance-terminal state per `concept:terminal-resolution`, then exits.

## Rationale

Park is handled by the supervisor's existing policy (the time-wake at the park's required resume-at), so parked nodes do not require special verb-level handling. The supervisor's existing instance-terminal promotion path is the natural completion gate.
