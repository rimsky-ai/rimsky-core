---
decision: graceful-shutdown
status: adopted
---

# Graceful shutdown with a hardcoded grace

## Choice

On interrupt, terminate, run-timeout expiry, or natural completion, shutdown is polite-then-forceful: new dispatches stop first, in-flight dispatches and spawned children receive a polite terminate signal, and anything still running after a five-second hardcoded grace is hard-killed before the remaining surfaces close and the process exits (the most-recent-run pointer updating per `decision:artifact-layout` on the way out). A second interrupt escalates to hard exit — immediate hard kill, best-effort close.

## Rationale

Five seconds is a conservative polite-terminate-then-hard-kill grace — well-behaved executors unwind within it, misbehaving ones get hard-killed without blocking the operator. The second-interrupt escape hatch is the conventional "I really mean it" fallback.

## Alternatives

- A configurable grace period — rejected: a knob for a window well-behaved executors never approach; one conservative hardcoded value serves every deployment.
- Wait indefinitely for in-flight work to unwind — rejected: a single misbehaving executor blocks the operator's exit.
