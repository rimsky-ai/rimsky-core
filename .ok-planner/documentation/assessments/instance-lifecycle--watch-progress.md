---
assessment: instance-lifecycle--watch-progress
subject: story:instance-lifecycle
way: watch-progress
release: d977250c
outcome: held
warrant: experiment:instance-lifecycle
---
# Watching an instance's progress as it runs

After work was invoked, the event log reported the node's `catalog:event-kinds/work_completed`, `catalog:cli-verbs/rimsky instance nodes` reported the node fresh, and `catalog:cli-verbs/rimsky instance status` reported its terminal success signal. Progress is therefore readable at each step and from more than one angle — the feed for what happened, the node listing for where each node stands, and the status verb for the instance's own verdict — without the operator correlating them by hand.

## Unverified remainder

None: the passing run demonstrates the way as promised.
