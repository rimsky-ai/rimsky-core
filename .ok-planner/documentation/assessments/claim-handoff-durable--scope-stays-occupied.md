---
assessment: claim-handoff-durable--scope-stays-occupied
subject: story:claim-handoff-durable
way: scope-stays-occupied
release: d977250c
outcome: held
warrant: experiment:claim-handoff-durable
---
# While the durable claim stands, nobody else gets the scope

A competing instance asking for the same scope while the durable claim stood was refused at acquisition, and it was refused again after the holding instance had been terminated, for as long as the claim handle remained. The producer therefore still occupies the scope between dispatches, which is the guarantee that makes a durable claim worth taking: the workflow can come back to what it claimed and find it unchanged by anybody else.

## Unverified remainder

None: the passing run demonstrates the way as promised.
