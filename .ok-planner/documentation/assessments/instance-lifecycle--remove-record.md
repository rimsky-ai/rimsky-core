---
assessment: instance-lifecycle--remove-record
subject: story:instance-lifecycle
way: remove-record
release: d977250c
outcome: held
warrant: experiment:instance-lifecycle
---
# Removing an instance's record once it is done

`catalog:cli-verbs/rimsky instance delete` was issued on both terminated instances, and afterwards neither appeared in `catalog:cli-verbs/rimsky instance list`. The record goes away on the operator's word rather than lingering, so a deployment an operator works in daily does not accumulate finished instances they have to read past.

## Unverified remainder

None: the passing run demonstrates the way as promised.
