---
assessment: sub-claim-payload-substitution--regular-claim
subject: story:sub-claim-payload-substitution
way: regular-claim
release: d977250c
outcome: held
warrant: experiment:sub-claim-payload-substitution
---
# Reading a claim's producer-supplied payload on a node holding a regular claim

The audit ran a template through the control API of a deployment carrying `catalog:bundled-services/claim-producer-filesystem`, whose declared pick policies supply a payload per claimed item. On a node holding a claim opened directly, the field path resolved to that claim's payload field and the bare path resolved to the whole payload object. This is the baseline the story's identity claim is measured against: the author writes the standard claim-payload directive and gets the producer's data for the claim the node holds.

## Unverified remainder

One producer and two directive forms — a field path and the whole payload — were exercised. The demonstration does not establish nested field paths deeper than one level.
