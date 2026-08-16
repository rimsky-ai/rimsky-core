---
assessment: lineage-exploration--pivot-by-source
subject: story:lineage-exploration
way: pivot-by-source
release: d977250c
outcome: held
warrant: experiment:lineage-exploration
---
# Asking which runs drew from a given source

An operator who knows a source rather than a run can start there. The audit queried `catalog:http-routes/GET /v1/lineage/by-source/{source_type}/{source_id}` with the attribute the consuming node had substituted, and the answer was that consuming node's lineage record. Querying by the producing run as a source likewise returned the consuming run's record, so the pivot resolves the same relationship the forward walk does, reached from the other end. This is the entry point that turns "where did this value come from" into a single read rather than a scan.

## Unverified remainder

One source type was pivoted on in this run. The pivot was not enumerated over every source type a deployment can record.
