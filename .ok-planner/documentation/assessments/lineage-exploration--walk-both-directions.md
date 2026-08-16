---
assessment: lineage-exploration--walk-both-directions
subject: story:lineage-exploration
way: walk-both-directions
release: d977250c
outcome: held
warrant: experiment:lineage-exploration
---
# Walking a run's lineage backward to what fed it and forward to what it fed

The audit drove a workflow on an all-in-one deployment (`catalog:images/rimsky-all-in-one`) in which a producing node holds a claim and fans out over two partitions while a consuming node substitutes an attribute from it, so one run leaves two runs joined by a substitution. Reading the consuming run by its own id through `catalog:http-routes/GET /v1/lineage/runs/{run_id}` returned that run's lineage record, whose substitution references name the upstream producing run. Walking that run backward through `catalog:http-routes/GET /v1/lineage/runs/{run_id}/ancestors` reached the producing run, and walking the producing run forward through `catalog:http-routes/GET /v1/lineage/runs/{run_id}/descendants` reached the consuming run, so the trace closes in both directions on the same pair. A depth given by the caller was honoured in the answer rather than ignored. A run id with no lineage answered not-found rather than an empty walk, so an operator can tell "nothing recorded" from "nothing upstream". Fourteen checks across this way and its siblings, none failing.

## Unverified remainder

The walk was demonstrated over a two-run graph one hop deep in each direction. Depth honouring was checked at one caller-given value, not swept across the range, and the walk was not exercised over a deep or wide lineage graph.
