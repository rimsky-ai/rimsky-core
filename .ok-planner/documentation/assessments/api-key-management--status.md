---
assessment: api-key-management--status
subject: story:api-key-management
way: status
release: d977250c
outcome: held
warrant: experiment:api-key-management
---
# Asking a deployment which auth posture it is in

`catalog:cli-verbs/rimsky auth status` was run against a fresh deployment of `catalog:images/rimsky-all-in-one` before anything was minted, and it reported the deployment anonymous with zero keys. The same verb was re-run after each later act in the lifecycle and tracked them: authenticated with one key and one admin after bootstrapping, and authenticated with three keys and one admin at the end of the run. The operator therefore has one verb that answers both questions the posture raises — whether the deployment is still open, and how many credentials are outstanding.

## Unverified remainder

None: the passing run demonstrates the way as promised.
