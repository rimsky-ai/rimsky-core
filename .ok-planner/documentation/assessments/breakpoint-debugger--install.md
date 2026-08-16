---
assessment: breakpoint-debugger--install
subject: story:breakpoint-debugger
way: install
release: d977250c
outcome: held
warrant: experiment:breakpoint-debugger
---
# Installing a breakpoint on a running instance's checkpoint

The audit drove a container of `catalog:images/rimsky-all-in-one` on a template whose worker node subscribes to a counter and reads its count. One pause-mode pre-dispatch breakpoint was installed on the worker through `catalog:http-routes/POST /v1/instances/{idOrKey}/breakpoints` and read back off the instance through `catalog:http-routes/GET /v1/instances/{idOrKey}/breakpoints`. The worker's first dispatch stopped there: the held run read as running on the node view while the instance carried on, so the breakpoint holds one node's work rather than freezing the instance. Fourteen checks ran across the whole debugging session and none failed.

## Unverified remainder

The run installed one pause-mode breakpoint on a node's before-dispatch checkpoint; other checkpoint positions and modes the surface accepts were not exercised in this way.
