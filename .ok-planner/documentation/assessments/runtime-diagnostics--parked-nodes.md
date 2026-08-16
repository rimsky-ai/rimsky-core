---
assessment: runtime-diagnostics--parked-nodes
subject: story:runtime-diagnostics
way: parked-nodes
release: d977250c
outcome: held
warrant: experiment:runtime-diagnostics
---
# Reading which nodes are parked on a wedged instance

The audit wedged an instance deliberately rather than simulating one: a claim-holding node parked and did not return, a second node co-held its claim, and a receiver declared a force-refreshed dependency on the parked node. Read through `catalog:http-routes/GET /v1/admin/diagnostics/parked-nodes`, the park roster named exactly the one wedged node, identified it as the claim holder, and carried both when it parked and when it is due back — so the operator gets the reason and the horizon, not just a name. `catalog:cli-verbs/rimsky parked list` returned the same single row for the same node. Twenty-three checks across this way and its siblings, none failing.

## Unverified remainder

One parked node on one wedged instance was read. The way does not establish the roster's shape across many parked nodes or many instances at once.
