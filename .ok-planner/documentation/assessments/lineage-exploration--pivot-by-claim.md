---
assessment: lineage-exploration--pivot-by-claim
subject: story:lineage-exploration
way: pivot-by-claim
release: d977250c
outcome: held
warrant: experiment:lineage-exploration
---
# Pivoting into lineage from a claim handle, and following a claim to its sub-claims

The producing node in the audited workflow holds a claim from the bundled filesystem claim producer (`catalog:bundled-services/claim-producer-filesystem`) and fans out over two partitions, so the run leaves one claim split into two sub-claims. Reading that claim handle through `catalog:http-routes/GET /v1/lineage/claims/{claim_handle_id}` returned its record carrying the name of the producer that committed it and the outcome it settled at. Walking the claim forward through `catalog:http-routes/GET /v1/lineage/claims/{claim_handle_id}/descendants` reached both sub-claims, and walking one sub-claim backward through `catalog:http-routes/GET /v1/lineage/claims/{claim_handle_id}/ancestors` reached the claim it was split from. The claim is therefore a first-class entry point into the trace, not only something a run record mentions in passing.

## Unverified remainder

The split was one claim into two sub-claims at a single level. Deeper sub-claim chains and claims held by producers other than the bundled filesystem producer were not walked.
