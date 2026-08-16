---
assessment: instance-lifecycle--resume
subject: story:instance-lifecycle
way: resume
release: d977250c
outcome: held
warrant: experiment:instance-lifecycle
---
# Resuming a paused instance

`catalog:http-routes/POST /v1/instances/{idOrKey}/resume` reported the instance resumed, and the work held during the pause then ran through to `catalog:event-kinds/work_completed`. Resuming therefore releases exactly what the pause held, so an operator can stop an instance to inspect it and put it back to work without re-sending anything.

## Unverified remainder

None: the passing run demonstrates the way as promised.
