---
assessment: executor-reads-dispatch-context--recovered-predecessor
subject: story:executor-reads-dispatch-context
way: recovered-predecessor
release: d977250c
outcome: held
warrant: experiment:executor-reads-dispatch-context
---
# Reading that this dispatch follows one the runtime recovered

A node declaring `catalog:template-keys/nodes[].max_quiet_period` blocked without reporting on a dispatch that had no predecessor, so the runtime reaped the quiet dispatch and re-dispatched the same node-run. On that second dispatch the script read a non-null predecessor id and a predecessor disposition naming a stale recovery, and it reported success on the branch its own code takes only when a predecessor is present. The recovery path is therefore visible to the agent author from the dispatch context alone, at the moment the script starts, rather than reconstructed afterwards.

## Unverified remainder

None: the passing run demonstrates the way as promised.
