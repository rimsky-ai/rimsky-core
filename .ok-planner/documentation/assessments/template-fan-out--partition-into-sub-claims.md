---
assessment: template-fan-out--partition-into-sub-claims
subject: story:template-fan-out
way: partition-into-sub-claims
release: d977250c
outcome: held
warrant: experiment:template-fan-out
---
# A declared fan-out splits one claim into one work unit per partition

The audit ran a template declaring a fan-out node against `catalog:bundled-services/claim-producer-filesystem` over a workspace. The producer's split returned one sub-scope per declared partition, and the dispatch recorded three sub-claims keyed by partition. The author expressed the partitioning as a single template declaration — no per-partition node, no external splitter — and the parent plus its three clones all settled fresh.

## Unverified remainder

Three partitions over one producer were exercised. The demonstration does not establish behaviour when the producer returns no partitions at all.
