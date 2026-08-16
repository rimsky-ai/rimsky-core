---
assessment: subscriber-lineage-receiver--run-dag-and-data-lineage
subject: story:subscriber-lineage-receiver
way: run-dag-and-data-lineage
release: d977250c
outcome: held
warrant: experiment:subscriber-lineage-receiver
---
# Both the run DAG and the data lineage surface in the receiver

The audit read the deliveries back and found both halves of what a governance platform needs. The node events carry rimsky run facets naming the frame, and the consuming node's event carries the substitution reference naming the upstream run its input came from — so the edge between two runs travels with the event rather than having to be reconstructed. The claim the producing node committed appears as an output dataset in the claim producer's namespace, and the same producer appears as an input dataset on the producing node's event, which is the data side of the same picture.

## Unverified remainder

One workflow with two nodes, one message and one claim was exercised. The demonstration does not establish how a fan-out's sub-claims or a delegating node's sub-graph appear to the receiver.
