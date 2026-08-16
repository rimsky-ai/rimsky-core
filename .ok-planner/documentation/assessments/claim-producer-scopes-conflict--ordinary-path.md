---
assessment: claim-producer-scopes-conflict--ordinary-path
subject: story:claim-producer-scopes-conflict
way: ordinary-path
release: d977250c
outcome: held
warrant: experiment:claim-producer-scopes-conflict
---
# The producer's own overlap rule decides who gets the claim

A producer advertising the scopes-conflict capability defined overlap as two selectors ending in the same path segment, so two scopes can be byte-unequal and still overlap, and it recorded every conflict question rimsky put to it with the answer it gave. One instance took a durable claim, whose handle reads durable and committed, so its scope stayed occupied after the node settled. A second instance asked for a byte-unequal scope that is neither a prefix nor a suffix of the held one: the record shows rimsky putting the pair to the producer, the producer answering that they overlap, and the node being refused at acquisition — the two writers could not both hold claims. A third instance asked for a byte-unequal, non-overlapping scope and got its claim straight away, so the rule discriminates rather than refusing everything.

## Unverified remainder

None: the passing run demonstrates the way as promised.
