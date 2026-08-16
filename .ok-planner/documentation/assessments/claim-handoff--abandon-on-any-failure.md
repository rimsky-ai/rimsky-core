---
assessment: claim-handoff--abandon-on-any-failure
subject: story:claim-handoff
way: abandon-on-any-failure
release: d977250c
outcome: held
warrant: experiment:claim-handoff
---
# One node failing abandons the whole shared claim

The same template was run again with its last co-holder failing. The subgraph still opened the claim exactly once, and this time the producer received Abandon and no Commit. All three nodes — the acquirer and both co-holders — settled failed, and the claim handle ended abandoned. That is the all-or-nothing half of the promise: a stage-then-write-then-verify pipeline built this way cannot leave a partially written claim committed because one member of the subgraph did not succeed.

## Unverified remainder

None: the passing run demonstrates the way as promised.
