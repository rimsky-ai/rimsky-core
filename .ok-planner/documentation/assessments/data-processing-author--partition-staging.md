---
assessment: data-processing-author--partition-staging
subject: story:data-processing-author
way: partition-staging
release: d977250c
outcome: held
warrant: experiment:data-processing-author
---
# Staging one candidate per fan-out partition, finalized or collected

Two fan-out nodes over that producer made the deployment split twice and open one staging candidate per partition — five in all across a three-way and a two-way split — keyed by the partition names the producer itself had returned. The fan-out whose children settled had its three candidates committed; the fan-out whose children errored had its two abandoned. Nothing was left staged either way, and exactly three versions existed afterwards, one per committed partition. A reader of the author's store therefore sees a partition's data only once that partition is complete, and a failed write leaves nothing behind to clean up.

## Unverified remainder

None: the passing run demonstrates the way as promised.
