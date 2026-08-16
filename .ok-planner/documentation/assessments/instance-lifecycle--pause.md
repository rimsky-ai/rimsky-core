---
assessment: instance-lifecycle--pause
subject: story:instance-lifecycle
way: pause
release: d977250c
outcome: held
warrant: experiment:instance-lifecycle
---
# Pausing a running instance

`catalog:http-routes/POST /v1/instances/{idOrKey}/pause` reported the instance paused and it read back paused. A message posted while it was paused was queued but never marked delivered, and no work ran, so pausing actually holds the instance rather than only labelling it. The queued work was still there to be released, so nothing posted during the pause was lost.

## Unverified remainder

None: the passing run demonstrates the way as promised.
