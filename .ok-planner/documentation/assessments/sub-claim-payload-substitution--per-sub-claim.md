---
assessment: sub-claim-payload-substitution--per-sub-claim
subject: story:sub-claim-payload-substitution
way: per-sub-claim
release: d977250c
outcome: held
warrant: experiment:sub-claim-payload-substitution
---
# The same directive reads each sub-claim's own payload inside a fan-out

The audit drove two nodes carrying byte-identical attribute sources, differing only in how the claim arrives. In the fan-out, three sub-claims were opened and each clone settled carrying the payload of its own sub-claim: the resolved field values equalled the set of sub-claim partition keys, no two clones resolved the same value, and the bare path returned the same object whose field the field path had returned. The resolved attribute shape was identical in the fan-out and on the regular claim, and the parent and all three clones settled fresh. A template author therefore learns one directive, not two.

## Unverified remainder

None: the passing run demonstrates the way as promised.
