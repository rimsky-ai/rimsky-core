---
assessment: claim-producer-protocol--handle-drives-dispatch
subject: story:claim-producer-protocol
way: handle-drives-dispatch
release: d977250c
outcome: held
warrant: experiment:claim-producer-protocol
---
# The claim handle the producer returns reaches the node's dispatch

Four nodes, one per write semantics, each settled fresh. What each producer returned on Open arrived in the node's resolved attributes: the address, the scope bytes, and a named field of the payload the producer synthesized. The producer's return value is therefore the node's input, which is what lets an author put store-specific detail — a connection string, a staging location, a row payload — in front of the executor without the template author knowing anything about the store.

## Unverified remainder

None: the passing run demonstrates the way as promised.
