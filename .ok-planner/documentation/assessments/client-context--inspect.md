---
assessment: client-context--inspect
subject: story:client-context
way: inspect
release: d977250c
outcome: held
warrant: experiment:client-context
---
# Seeing which deployments are registered and which one is current

`catalog:cli-verbs/rimsky ctx list` showed both registered names with their endpoints and marked the current one, and `catalog:cli-verbs/rimsky ctx current` named it on its own. An operator with several deployments on one machine can therefore answer "where would this command go" before running the command, which is the question that makes a context feature safe to use.

## Unverified remainder

None: the passing run demonstrates the way as promised.
