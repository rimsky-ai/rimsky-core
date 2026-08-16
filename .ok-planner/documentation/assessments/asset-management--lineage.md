---
assessment: asset-management--lineage
subject: story:asset-management
way: lineage
release: d977250c
outcome: held
warrant: experiment:asset-management
---
# Tracing what consumed an asset before retiring it

`catalog:cli-verbs/rimsky asset lineage` walked backward from the asset to what produced it, and the forward walk from the asset's materializing run — through `catalog:http-routes/GET /v1/lineage/runs/{run_id}/descendants` and `catalog:http-routes/GET /v1/lineage/by-source/{source_type}/{source_id}` — reached the downstream leaf run that had consumed the asset. Both directions were checked on the same asset. This is the "what depends on this before I retire it" the story asks for: the operator sees the consumer, not only the producer, before acting.

## Unverified remainder

The topology walked was one producing node and one consuming node; the run does not establish how a deeper or branching consumer graph renders.
