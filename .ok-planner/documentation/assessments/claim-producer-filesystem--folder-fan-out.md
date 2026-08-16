---
assessment: claim-producer-filesystem--folder-fan-out
subject: story:claim-producer-filesystem
way: folder-fan-out
release: d977250c
outcome: held
warrant: experiment:claim-producer-filesystem
---
# Fanning work out over what is already in the store

A second node claimed a directory that already held three files and declared a fan-out over it. The producer's split returned three sub-scopes; the partition keys are the three file names, and each work unit's claim addressed its own file. The parent and all three work units settled fresh. The partitioning therefore comes from the store's own contents rather than from something the template author has to enumerate in advance, which is the point of putting the fan-out on a filesystem producer.

## Unverified remainder

None: the passing run demonstrates the way as promised.
