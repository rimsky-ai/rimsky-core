---
assessment: template-fan-out--parent-settles-on-aggregate
subject: story:template-fan-out
way: parent-settles-on-aggregate
release: d977250c
outcome: held
warrant: experiment:template-fan-out
---
# The parent settles once its sub-claims resolve, on their aggregate verdict

The audit followed the parent's settlement in event order: it follows the last sub-claim's resolution, so the parent waits for the partitions rather than racing them. With the work endpoint failing every partition, no run settled fresh, the parent settled failed naming the aggregation verdict, and the partitions' claims were abandoned (`catalog:event-kinds/claim_resolution.abandon`) rather than committed — so a failed fan-out leaves no half-committed work behind.

## Unverified remainder

Two aggregate outcomes — all partitions succeeding and all failing — were exercised. The demonstration does not establish the parent's verdict on a mixed outcome where some partitions succeed and others fail.
