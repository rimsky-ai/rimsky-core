---
assessment: client-context--switch
subject: story:client-context
way: switch
release: d977250c
outcome: held
warrant: experiment:client-context
---
# Switching which deployment commands go to

`catalog:cli-verbs/rimsky ctx use` switched the current context, and the switch was settled by consequence rather than by its own output: with no endpoint named anywhere, `catalog:cli-verbs/rimsky ls templates` returned the first deployment's template and not the second's, and after the switch returned the second's and not the first's. The current context therefore really decides which deployment answers, rather than only recording a preference.

## Unverified remainder

None: the passing run demonstrates the way as promised.
