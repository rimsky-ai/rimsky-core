---
assessment: operator-invalidate-queues-during-flight--queued-behind-inflight
subject: story:operator-invalidate-queues-during-flight
way: queued-behind-inflight
release: d977250c
outcome: held
warrant: experiment:operator-invalidate-queues-during-flight
---
# Forcing a re-run of a node that already has a run in flight

The audit held a worker's run in flight at a pause-mode pre-dispatch breakpoint (`catalog:http-routes/POST /v1/instances/{idOrKey}/breakpoints`) on an all-in-one deployment, then invalidated that same worker through `catalog:http-routes/POST /v1/instances/{id}/debug/override` while its run sat there. The call was accepted, reporting one run mutated, and a second worker run appeared queued — while the run already in flight was still the same run in the same state, dispatched once. The operator's action was therefore neither silently dropped nor destructive to the work already under way. Releasing the hit let the in-flight run settle successfully, and only then did the queued run dispatch: the second breakpoint hit named it, and its dispatch follows the first run's completion in the event sequence. Both runs reached success. Eight checks, none failing.

## Unverified remainder

One invalidate was issued against one in-flight run. The way does not establish what several invalidates issued against the same in-flight run produce, nor the outcome when the in-flight run fails rather than succeeds.
