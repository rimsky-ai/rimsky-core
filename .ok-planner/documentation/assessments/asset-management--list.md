---
assessment: asset-management--list
subject: story:asset-management
way: list
release: d977250c
outcome: held
warrant: experiment:asset-management
---
# Listing the data assets an instance has produced

The audit drove a deployment of `catalog:images/rimsky-all-in-one` wired to a claim producer that advertises data processing and mints a version on each commit, on a template whose node opens a durable claim and whose downstream node reads that node's output. `catalog:cli-verbs/rimsky asset list` returned exactly one asset for the instance — the durable claim, named by the node type and the claim alias that produced it. One durable claim, one asset row: the listing is the operator's inventory of what the instance actually put somewhere, not a listing of every claim the run touched.

## Unverified remainder

None: the passing run demonstrates the way as promised.
