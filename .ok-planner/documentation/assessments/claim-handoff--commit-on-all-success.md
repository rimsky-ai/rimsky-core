---
assessment: claim-handoff--commit-on-all-success
subject: story:claim-handoff
way: commit-on-all-success
release: d977250c
outcome: held
warrant: experiment:claim-handoff
---
# One claim, shared across a subgraph, committed once when every node succeeds

The audit drove a deployment of `catalog:images/rimsky-all-in-one` against a claim producer written for the run, on a template where an acquirer node opens a claim on a selector and two downstream nodes co-hold that same claim. On the all-success run, the producer received exactly one Open for the whole holding subgraph and exactly one Commit, with no Abandon: the claim is taken once for the shape, not once per node. Both co-holders received the acquirer's address byte-for-byte, along with a payload field and the scope bytes, so neither re-acquired anything to do its work against the staged location. The claim handle ended committed, and `catalog:http-routes/GET /v1/claim-handles/{claim_handle_id}/holders` reported three holders on that one claim.

## Unverified remainder

None: the passing run demonstrates the way as promised.
