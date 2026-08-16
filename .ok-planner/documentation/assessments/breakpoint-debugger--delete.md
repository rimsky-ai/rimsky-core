---
assessment: breakpoint-debugger--delete
subject: story:breakpoint-debugger
way: delete
release: d977250c
outcome: held
warrant: experiment:breakpoint-debugger
---
# Removing a breakpoint and clearing its hits

`catalog:http-routes/DELETE /v1/instances/{idOrKey}/breakpoints/{breakpoint_id}` answered no-content, removed the breakpoint from the instance's breakpoint listing, and emptied the hits ledger with it, so ending a debugging session does not leave stale stops behind. The instance's own history is not rewritten by that cleanup: the `catalog:event-kinds/breakpoint.hit` record in the event log survived the deletion, so what happened during the session stays auditable after the session's apparatus is gone.

## Unverified remainder

None: the passing run demonstrates the way as promised.
