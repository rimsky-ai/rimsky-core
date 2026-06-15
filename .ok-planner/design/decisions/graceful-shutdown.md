---
decision: graceful-shutdown
status: adopted
---

# graceful-shutdown

## Choice

On interrupt, terminate, run-timeout expiry, or natural completion: supervisor stops new dispatches → in-flight dispatches and spawned children receive a polite terminate signal → a five-second hardcoded grace → a hard kill on anything still running → control-api stops → SQL connection closes → the most-recent-run pointer updates per `decision:artifact-layout` → exit. A second interrupt escalates to hard exit (immediate hard kill, best-effort close).

## Rationale

Five seconds is a conservative polite-terminate-then-hard-kill grace — well-behaved executors unwind within it, misbehaving ones get hard-killed without blocking the operator. The second-interrupt escape hatch is the conventional "I really mean it" fallback.
