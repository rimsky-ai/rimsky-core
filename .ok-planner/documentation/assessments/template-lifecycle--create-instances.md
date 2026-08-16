---
assessment: template-lifecycle--create-instances
subject: story:template-lifecycle
way: create-instances
release: d977250c
outcome: held
warrant: experiment:template-lifecycle
---
# Creating live instances of a ready definition

Once the definition was marked ready, `catalog:cli-verbs/rimsky instance create` produced a live instance of it. That live instance is what the rest of the lifecycle is measured against: while it existed, retiring and removing the definition were both refused. Creation and the catalogue's later states are therefore connected, not independent switches.

## Unverified remainder

One live instance was created. The demonstration does not establish the catalogue's behaviour with many concurrent instances of one definition.
