---
assessment: claim-producer-scopes-conflict--fan-out-path
subject: story:claim-producer-scopes-conflict
way: fan-out-path
release: d977250c
outcome: held
warrant: experiment:claim-producer-scopes-conflict
---
# The same rule governs sub-claims a fan-out asks for

A fan-out asked for two sub-claims that are byte-unequal but overlapping under the producer's rule. The record shows that pair being put to the producer on the fan-out path too, and the producer calling them overlapping. Neither sub-claim ended up with a claim handle and the fan-out settled neither partition. The no-overlapping-writers rule therefore holds where an operator is most likely to generate overlapping scopes by accident — a fan-out that partitions a scope — and not only on claims a template names one at a time.

## Unverified remainder

None: the passing run demonstrates the way as promised.
