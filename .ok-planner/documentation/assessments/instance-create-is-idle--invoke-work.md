---
assessment: instance-create-is-idle--invoke-work
subject: story:instance-create-is-idle
way: invoke-work
release: d977250c
outcome: held
warrant: experiment:instance-create-is-idle
---
# Invoking work on the instance as a separate operator action

Posting a message to the untouched instance through `catalog:http-routes/POST /v1/instances/{id}/messages` drove it to completion, so the work an operator expects is available on demand and was only ever waiting to be asked for. Creating and invoking are therefore two acts the operator drives independently, and the second is what makes anything run. The completion was read back from the instance's own event log.

## Unverified remainder

None: the passing run demonstrates the way as promised.
