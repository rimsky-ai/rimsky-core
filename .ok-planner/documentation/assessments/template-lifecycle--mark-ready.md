---
assessment: template-lifecycle--mark-ready
subject: story:template-lifecycle
way: mark-ready
release: d977250c
outcome: held
warrant: experiment:template-lifecycle
---
# Marking a registered definition ready to run

The audit tried to create an instance before and after marking the definition ready with `catalog:cli-verbs/rimsky template deploy`. Creation was refused before and accepted after, so being in the catalogue and being runnable are separate states the operator controls. A definition can therefore be registered and reviewed without anyone being able to start work from it yet.

## Unverified remainder

None: the passing run demonstrates the way as promised.
