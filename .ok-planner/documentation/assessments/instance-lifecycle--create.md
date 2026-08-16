---
assessment: instance-lifecycle--create
subject: story:instance-lifecycle
way: create
release: d977250c
outcome: held
warrant: experiment:instance-lifecycle
---
# Creating a live instance of a deployed template

`catalog:cli-verbs/rimsky instance create` against a container of `catalog:images/rimsky-all-in-one` returned an instance id and materialized the instance's root node, so the operator has a live thing to drive from the moment the call returns. The instance was created from a deployed template with no further setup. Everything the rest of the lifecycle does was then driven against that instance and a second one created the same way.

## Unverified remainder

None: the passing run demonstrates the way as promised.
