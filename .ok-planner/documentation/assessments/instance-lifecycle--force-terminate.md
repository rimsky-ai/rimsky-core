---
assessment: instance-lifecycle--force-terminate
subject: story:instance-lifecycle
way: force-terminate
release: d977250c
outcome: held
warrant: experiment:instance-lifecycle
---
# Force-terminating an instance that is wedged

`catalog:http-routes/POST /v1/instances/{idOrKey}/terminate` stamped the first instance terminated and it read back terminated. `catalog:cli-verbs/rimsky instance kill` with `catalog:cli-flags/--force` did the same for the second, so the operator has the same intervention from the CLI and from the route. Both instances stayed terminated on re-read, which is what an operator needs when the ordinary path is not answering.

## Unverified remainder

None: the passing run demonstrates the way as promised.
