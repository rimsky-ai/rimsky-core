---
assessment: asset-management--materialization-audit
subject: story:asset-management
way: materialization-audit
release: d977250c
outcome: held
warrant: experiment:asset-management
---
# Reading the audit of how an asset came to be

`catalog:http-routes/GET /v1/instances/{id}/assets/{alias}/materialization-history` returned the claim's terminal records for the asset, and every row it returned was of that kind — the audit is a population, checked whole, not a sample. This is the record that says when the asset was materialized and how the claim behind it resolved, which is what an operator needs before deciding whether a re-materialization is safe.

## Unverified remainder

None: the passing run demonstrates the way as promised.
