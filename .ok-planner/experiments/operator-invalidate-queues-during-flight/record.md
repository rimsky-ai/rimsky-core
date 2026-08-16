---
experiment: operator-invalidate-queues-during-flight
commit: d977250c
---

# An operator invalidate against a node whose run is in flight

## What it ran against

`way-queued-behind-inflight.py` boots a `rimsky-all-in-one` container at
`RIMSKY_IMAGE_TAG` and drives it through the control API. The template declares a
counter node and a worker node that subscribes to it. A pause-mode pre-dispatch
breakpoint on the worker holds the worker's first run in flight, and the script
invalidates the worker itself through the debug-override channel while that run
sits there.

## What was observed

At the moment of the invalidate the worker had exactly one run, in `running`.
The invalidate answered 200 with one run mutated and produced a second worker
run in `stale`; the run already in flight was still the same run, still
`running`, and the worker had been dispatched only once. Releasing the hit let
the in-flight run settle successfully, and the queued run dispatched after that
settlement, not before — the second breakpoint hit named the queued run, and its
dispatch followed the first run's completion in the event log. Both runs reached
success carrying the same upstream value.

Eight checks, none failing.

RESULT: PASS
