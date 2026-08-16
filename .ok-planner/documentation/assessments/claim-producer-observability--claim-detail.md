---
assessment: claim-producer-observability--claim-detail
subject: story:claim-producer-observability
way: claim-detail
release: d977250c
outcome: held
warrant: experiment:claim-producer-observability
---
# Fetching one claim's full detail off the producer

The audit ran `catalog:images/rimsky-claim-producer-filesystem` as its own container over a seeded workspace, drove its observability protocol from a dashboard-shaped client built for the run, and pointed a deployment of `catalog:images/rimsky-all-in-one` at the same producer. Thirty-four checks ran across the four capabilities and none failed. A claim's detail came back open, carrying its scope, the time it was opened, and its event history — enough for a dashboard to render one claim's story without asking rimsky for anything. The data comes from the producer, which is the party that actually knows it.

## Unverified remainder

None: the passing run demonstrates the way as promised.
