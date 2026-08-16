---
assessment: all-upstream-gating--fan-in-gating
subject: story:all-upstream-gating
way: fan-in-gating
release: d977250c
outcome: held
warrant: experiment:all-upstream-gating
---
# A fan-in receiver waits for every upstream still in flight

The audit drove a template whose fan-in receiver subscribes to two upstream siblings, on a deployment of `catalog:images/rimsky-all-in-one`, through the control API. The two upstreams went stale by different routes: one is a structural root woken by an operator message, the other is woken by cascade from a third node and then made stale again by an operator invalidation. One upstream was held in flight at a pause-mode pre-dispatch breakpoint while the other settled twice, and across both of those settlements the receiver never dispatched. Deleting the breakpoint through `catalog:http-routes/DELETE /v1/instances/{idOrKey}/breakpoints/{breakpoint_id}` released the held upstream; the receiver then dispatched exactly once, after the last upstream completion in the event sequence, and its outcome carried values from both upstreams. Seven checks ran and none failed.

## Unverified remainder

None: the passing run demonstrates the way as promised.
