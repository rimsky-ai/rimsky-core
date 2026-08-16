---
assessment: asset-management--current-version
subject: story:asset-management
way: current-version
release: d977250c
outcome: held
warrant: experiment:asset-management
---
# Seeing what an asset currently is

`catalog:cli-verbs/rimsky asset show` returned the asset's detail carrying the version id the producer minted at commit, the asset's committed state, and its durable lifetime. The version is the producer's own value passed through rather than a number rimsky assigns, so the operator reads the same identity the store behind the asset would report. Together the three fields answer whether the asset is finished, whether it will outlive the dispatch that made it, and which revision of it is in place.

## Unverified remainder

None: the passing run demonstrates the way as promised.
