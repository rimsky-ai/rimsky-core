---
decision: debug-channel-gate-paused-or-breakpoint
status: as-is
---

# Debug-channel gate: paused or breakpoint

## Choice

The control-API debug-override endpoint is legal iff the instance is paused (the existing instance-level pause flag is true) OR the instance holds at least one unresumed pause-mode breakpoint hit blocking a runner. Otherwise the request returns a conflict response.

## Rationale

The override is a debug feature, not a general operator capability. Both legal states are operator-engineered — the operator deliberately paused or set a breakpoint — so the override is contextually expected. Healthy frames have no override path; the operator must opt in to debug mode first.

## Alternatives

- Including "frame held by parked node-run" in the gate — rejected: that is a normal-operation degraded state, not an error; the project's pre-v1 rule is to investigate and fix the underlying issue rather than engineer around it with an override.
