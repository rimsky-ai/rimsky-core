---
assessment: fanout-any-substitution-source--instance-param
subject: story:fanout-any-substitution-source
way: instance-param
release: d977250c
outcome: held
warrant: experiment:fanout-any-substitution-source
---
# Writing a fan-out partition request that reads from an instance param

With `catalog:template-keys/nodes[].fan_out.partition_request` reading from an instance param, the fan-out dispatched exactly the two partitions the param named, and both work units resolved their own partition keys into their attribute bags. The run recorded no resolution error, and the count of work units reporting a key equalled the count the source named. The partition set is therefore decided at instance creation by whoever supplies the params, without the template changing.

## Unverified remainder

None: the passing run demonstrates the way as promised.
