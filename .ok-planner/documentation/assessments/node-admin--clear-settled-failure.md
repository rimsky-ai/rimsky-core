---
assessment: node-admin--clear-settled-failure
subject: story:node-admin
way: clear-settled-failure
release: d977250c
outcome: held
warrant: experiment:node-admin
---
# Clearing a stale failure marker off a settled node, and nothing else

Clearing through `catalog:http-routes/POST /v1/nodes/{id}/reset` was refused on the node that never failed, naming the condition, so the operation is guarded rather than blanket. On the failed node it succeeded, driven through `catalog:cli-verbs/rimsky admin reset`. Reading the same node afterwards showed no settled failure signal at all, while its identity, executor, run tallies and the check's findings were unchanged — so the failure marker is gone and nothing else about the node's readable state moved, which is what lets an operator read the node's true current state while deciding how to intervene. The clearing itself appears on the instance's event log at `catalog:http-routes/GET /v1/events` as one operator-override entry against that node, so the intervention is on the record.

## Unverified remainder

One failed node was cleared, once. The way does not establish what a second clearing, or a clearing while the node has a run in flight, does.
