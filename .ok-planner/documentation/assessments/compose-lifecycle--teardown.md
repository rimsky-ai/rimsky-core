---
assessment: compose-lifecycle--teardown
subject: story:compose-lifecycle
way: teardown
release: d977250c
outcome: held
warrant: experiment:compose-lifecycle
---
# Removing everything the manifest created with one command

With both instances driven terminal, one `catalog:cli-verbs/rimsky compose down` removed instances, deployments, tags, and templates in eight steps, and both the template and instance listings came back clean. The teardown covers every kind of resource the manifest declares, so an operator does not have to remember which verbs to run in which order to undo an apply.

## Unverified remainder

None: the passing run demonstrates the way as promised.
