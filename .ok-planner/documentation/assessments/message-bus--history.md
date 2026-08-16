---
assessment: message-bus--history
subject: story:message-bus
way: history
release: d977250c
outcome: held
warrant: experiment:message-bus
---
# Reading the instance's message history back

The audit read the instance's bus through `catalog:http-routes/GET /v1/instances/{id}/messages`. The history listed both distinct sends, each attributed to the operator who sent it, and — after three sends under one dedup key — held exactly one row for that key rather than three, so the history reflects the identities the bus actually minted. That is the capability the story names: an operator can see what has been sent into a live instance rather than inferring it from node behaviour.

## Unverified remainder

The history capability holds on the control-API route. The operator CLI's own history verb (`catalog:cli-verbs/rimsky messages tail`) does not deliver it without `catalog:cli-flags/--follow`: in the same run it printed only the newest row and dropped the older ones, because it de-duplicates against a watermark assuming ascending arrival while the route answers newest-first. An operator reading history through that verb alone will see less than the bus holds.
