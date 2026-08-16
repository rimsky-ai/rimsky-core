---
assessment: breakpoint-debugger--read-hits
subject: story:breakpoint-debugger
way: read-hits
release: d977250c
outcome: held
warrant: experiment:breakpoint-debugger
---
# Seeing the hits a breakpoint has taken

Both places the story names carried the hit. `catalog:http-routes/GET /v1/instances/{idOrKey}/breakpoint-hits` returned one hit naming the worker's node and the sealed dispatch bag as it stood when the run was stopped, and the unified event log carried the same hit as one `catalog:event-kinds/breakpoint.hit` record naming the breakpoint and the node. An operator therefore reads the same stop either from the ledger, which is about the debugging session, or from the event log, which is about the instance's whole history.

## Unverified remainder

None: the passing run demonstrates the way as promised.
