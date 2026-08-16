---
assessment: fanout-list-array--upstream-list
subject: story:fanout-list-array
way: upstream-list
release: d977250c
outcome: held
warrant: experiment:fanout-list-array
---
# Fanning out over a list an upstream node produced

A template whose first node writes a three-element list as its own attribute, and whose second node names that attribute as its `catalog:template-keys/nodes[].fan_out.partition_request` over a claim on the bundled `catalog:bundled-services/claim-producer-filesystem`, ran on a deployment that supplied no claim producer of its own — the only producer in play is the one the image ships. The producer split the parent claim into three sub-scopes and the fan-out dispatched three work units keyed by the three items the list declared. Each work unit resolved its own partition key into its attribute bag, and the three keys observed were exactly the three the list declared. The node's run summary reported four fresh runs — the parent plus its three work units — and no failures. A template author gets one parallel work unit per item without writing a claim producer. Six checks, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
