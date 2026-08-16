---
assessment: fanout-any-substitution-source--claim-payload
subject: story:fanout-any-substitution-source
way: claim-payload
release: d977250c
outcome: held
warrant: experiment:fanout-any-substitution-source
---
# Writing a fan-out partition request that reads from the claim's own payload

With `catalog:template-keys/nodes[].fan_out.partition_request` interpolating the claim's payload into its keys, the fan-out dispatched exactly the two partitions the payload named — the folder name the bundled `catalog:bundled-services/claim-producer-filesystem` put in the payload, carried into the keys the author wrote. Both work units resolved their own keys, the counts agreed, and no resolution error was recorded. The partitions can therefore be named partly by what the claim producer found, rather than only by what the template already knows.

## Unverified remainder

None: the passing run demonstrates the way as promised.
