---
assessment: executor-protocol--settling-outcomes
subject: story:executor-protocol
way: settling-outcomes
release: d977250c
outcome: held
warrant: experiment:executor-protocol
---
# Returning each terminal outcome my executor can settle on

A template written to the peer's advertisement registered, deployed and ran, and each outcome the executor returned over `catalog:grpc-rpcs/Executor.Execute` was honoured as the author meant it. The success outcome settled the node fresh with the peer's own attribute delta on the record, so what the executor computed reaches the graph. The error outcome settled the node failed. The park outcome parked the node instead of settling it, and the node carried the park signal, so an executor can suspend work rather than end it. All three readings came back through the ordinary instance and event surfaces, with nothing peculiar to a third-party executor.

## Unverified remainder

None: the passing run demonstrates the way as promised.
