---
assessment: claim-handoff-durable--outlives-dispatch
subject: story:claim-handoff-durable
way: outlives-dispatch
release: d977250c
outcome: held
warrant: experiment:claim-handoff-durable
---
# A durable claim survives the dispatch that took it

The audit drove a deployment of `catalog:images/rimsky-all-in-one` against a claim producer written for the run, on a template whose acquirer declares durable lifetime and whose co-holder shares the claim by alias. After the acquiring dispatch settled, exactly one claim handle for that scope remained, reading durable and committed, and the co-holder in that same dispatch had read the claim by alias while it ran. Nothing about the dispatch ending reaped the claim, which is what a workflow needs when the thing it claimed is an asset rather than a scratch area.

## Unverified remainder

None: the passing run demonstrates the way as promised.
