---
audit: operator-invalidate-queues-during-flight
artifact: story:operator-invalidate-queues-during-flight
determination: supported
commit: 3918d24e
audited: 2026-08-02T09:41:29Z
---

# Operator invalidate against an in-flight run queues rather than drops or destroys

Supported. `applyDebugOverride`/`setNodeAttributeForDebugOverride` (`lib/control/controlapi/debug_override.go`) drive the invalidate route: when the target node's latest run is dispatched in-flight (`running`/`held`/`parked`), the handler creates an additional `CreateNonCascadeStale` row with `creation_reason=operator_invalidate` alongside the in-flight run rather than cancelling or overwriting it. `test/scenarios/operator_invalidate_queues_during_flight_test.go::TestOperatorInvalidateQueuesDuringFlight` (tagged `@story: operator-invalidate-queues-during-flight`) proves the queuing behavior end-to-end against a real stack: with a worker parked mid-dispatch, an operator invalidate creates exactly one new `stale` row with `creation_reason=operator_invalidate` that carries the merged attribute bag forward; after resume, the test explicitly checks — over a quiesce window, not a single snapshot — that the queued run does NOT dispatch while the parked predecessor is still in flight (the dispatcher's serialization gate blocks it); only once the clock advances past the parked run's resume deadline does the predecessor settle and the queued run dispatch in turn, producing the lineage sequence `cascade,operator_invalidate` and exactly three total worker invocations. Both halves of the story's "so that" clause are directly checked: not silently dropped (the stale row and its dispatch are asserted) and not destructive to in-flight work (the parked run is allowed to resume and settle on its own schedule before the queued run fires).
