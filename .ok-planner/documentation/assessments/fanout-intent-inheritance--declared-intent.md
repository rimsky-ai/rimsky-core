---
assessment: fanout-intent-inheritance--declared-intent
subject: story:fanout-intent-inheritance
way: declared-intent
release: d977250c
outcome: held
warrant: experiment:fanout-intent-inheritance
---
# Declaring a fan-out claim read-only and trusting every sub-claim to inherit that

The same fan-out shape was driven under each value of `catalog:template-keys/nodes[].claim_producers[].intent` against a deployment running the bundled `catalog:bundled-services/claim-producer-filesystem` over a throwaway workspace. Under the read-only declaration the run opened one parent handle and three sub-handles, every sub-handle pointing at that parent and every one carrying the read-only intent, and all four acquisitions the run recorded named that intent and no other. Under the read-write declaration the same shape produced a parent and three sub-handles all carrying read-write. What the sub-claims carry therefore tracks what the template declared rather than a fixed producer default, which is the part a template author cannot check by reading the template. The handles and the acquisitions were read back through `catalog:http-routes/GET /v1/observability/claim-handles` and `catalog:http-routes/GET /v1/events`. Eight checks, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
