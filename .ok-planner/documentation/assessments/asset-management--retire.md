---
assessment: asset-management--retire
subject: story:asset-management
way: retire
release: d977250c
outcome: held
warrant: experiment:asset-management
---
# Retiring an asset once nothing needs it

`catalog:cli-verbs/rimsky asset delete` retired the asset and succeeded, and the effect was confirmed rather than assumed: `catalog:cli-verbs/rimsky asset list` no longer returned it. Retirement is the last step of the governing sequence the story describes — inventory, current version, history, audit, lineage, then retire — and it was reached by walking that whole sequence in one run.

## Unverified remainder

The run establishes that the asset leaves rimsky's inventory; what a producer does with the underlying data on retirement belongs to that producer.
