---
assessment: template-lifecycle--retire
subject: story:template-lifecycle
way: retire
release: d977250c
outcome: held
warrant: experiment:template-lifecycle
---
# Retiring a definition so no new instances start

The audit found retirement refused while an instance was live, and accepted once that instance was killed with `catalog:cli-verbs/rimsky instance kill`. After `catalog:cli-verbs/rimsky template undeploy` took effect, further instance creation was refused. An operator can therefore stop new work from starting without disturbing work already running, and the product will not let them do it out of order.

## Unverified remainder

None: the passing run demonstrates the way as promised.
