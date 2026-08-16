---
assessment: fanout-any-substitution-source--upstream-attribute
subject: story:fanout-any-substitution-source
way: upstream-attribute
release: d977250c
outcome: held
warrant: experiment:fanout-any-substitution-source
---
# Writing a fan-out partition request that reads from an upstream node's attribute

The same fan-out node was registered five times differing only in where its `catalog:template-keys/nodes[].fan_out.partition_request` reads from, against a deployment running the bundled `catalog:bundled-services/claim-producer-filesystem` over a throwaway workspace. Reading from an upstream node's attribute, the fan-out dispatched exactly the three partitions the attribute named, and each work unit resolved its own partition key into its own attribute bag, so the number of work units reporting a key equalled the number of partitions the source named. No run recorded a resolution error. A template author choosing this source writes the ordinary substitution grammar and nothing fan-out-specific. Ten checks across the whole story, none failing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
