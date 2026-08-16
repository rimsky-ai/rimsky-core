---
assessment: tag-management--re-point
subject: story:tag-management
way: re-point
release: d977250c
outcome: held
warrant: experiment:tag-management
---
# Rolling a name forward to a new definition, and back again

The audit re-pointed the name at the second hash with `catalog:cli-verbs/rimsky tag mv`. Resolving then returned the new hash and no longer the old one, and a newly created instance bound to the new hash — so the move governs what deploys next. The instance created before the move still reported the hash it was created from and was not terminated, which is the story's benefit clause measured rather than assumed: in-flight work is undisturbed by the roll. Re-pointing back resolved to the first hash again, so the roll is reversible in the same one act.

## Unverified remainder

None: the passing run demonstrates the way as promised.
