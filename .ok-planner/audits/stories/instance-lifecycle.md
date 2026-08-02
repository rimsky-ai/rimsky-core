---
audit: instance-lifecycle
artifact: story:instance-lifecycle
determination: supported
commit: b767a27d
audited: 2026-08-02T09:28:34Z
---

# Operator manages instance runtime lifecycle

Supported. `TestInstanceLifecycleFullStack` drives one instance through the full claimed lifecycle against the real control API: create (idle, then confirmed listed and gettable), watch progress (wakes it with an empty message and polls `/v1/events` for the `terminal/success` count to rise, using `WaitForNodeState` to confirm the fresh terminal), pause (asserts `paused=true`, then proves no new dispatch occurs and the wake message sits pending in `rimsky_messages` while paused — the frame engine is blocked from picking it up), resume (asserts `paused=false` and that the queued dispatch proceeds, producing a new `terminal/success`), terminate (asserts 200 and polls `rimsky_instances.terminated_at` for the terminal stamp), and delete (asserts a 409 terminal-guard on a live instance, then 200 plus `deleted:true` and a subsequent 404 once terminated). All six operator actions the story enumerates — create, watch, pause, resume, force-terminate, remove — are exercised against live control-API routes in one continuous run.
