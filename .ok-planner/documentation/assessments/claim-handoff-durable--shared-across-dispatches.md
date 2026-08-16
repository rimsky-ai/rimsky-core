---
assessment: claim-handoff-durable--shared-across-dispatches
subject: story:claim-handoff-durable
way: shared-across-dispatches
release: d977250c
outcome: held
warrant: experiment:claim-handoff-durable
---
# A later dispatch co-holds the same durable claim rather than taking a new one

A message of the template's declared type woke the co-holder alone into a second dispatch. It settled fresh, read the same claim address by alias, and registered as a third holder on the first dispatch's claim handle: one row for the scope throughout, the same row identity, and still exactly one Open at the producer across both dispatches. Later work in the same instance therefore joins the claim that already exists instead of competing for it.

## Unverified remainder

A co-holder that can only be woken by a later message never runs, because the acquirer's claim does not reach its auto-terminal while a member of its holding subgraph has not run; the shape demonstrated has the co-holder running in the acquiring dispatch as well as in the later one.
