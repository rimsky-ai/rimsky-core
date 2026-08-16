---
assessment: fanout-any-substitution-source--typed-message
subject: story:fanout-any-substitution-source
way: typed-message
release: d977250c
outcome: held
warrant: experiment:fanout-any-substitution-source
---
# Writing a fan-out partition request that reads from a typed message body

With `catalog:template-keys/nodes[].fan_out.partition_request` reading from a typed message's body, the fan-out dispatched exactly the three partitions the message named, each work unit resolved its own partition key, and the counts agreed with the source. No resolution error was recorded. A caller sending the message therefore decides the partition set at wake time, using the same grammar the other sources use.

## Unverified remainder

None: the passing run demonstrates the way as promised.
