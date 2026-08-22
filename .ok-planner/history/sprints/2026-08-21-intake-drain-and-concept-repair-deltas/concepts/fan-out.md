---
concept: fan-out
---

# Fan-out

## What it is

Fan-out clones the calling node N times. Each clone runs as the same template node type, with the same executor and the same attribute schema, in its own child run-scope. Nothing aggregates attributes across clones: each partition's attribute writeback stays its own, and fan-in happens at the claim handle under an author-chosen aggregation policy. Fan-out has nothing structurally to do with sub-graph delegation. A cloned node may itself delegate to a sub-graph, because composition layers the two, but the fan-out machinery operates on the calling node alone and never on a sub-graph's internals.

One parent claim splits into one sub-claim per partition, one clone dispatches per sub-claim, and the parent resolves from its clones' outcomes under the declared aggregation policy.

A template declares fan-out on a node, naming the claim to split, the partition request, the parallelism cap, and the aggregation policy. Fan-out is the aggregation policy's only consumer; delegation neither configures it nor reads it.

## Purpose

Fan-out lets one declared node cover a partitioned workload. The author writes the work once and leaves the number of partitions to the claim's producer, so the template never enumerates them and one parent claim governs the whole set.

## Boundaries

Fan-out owns the template surface that declares it, the partition cardinality that the producer decides when it splits, the split at parent acquisition, and the per-partition sub-claim asymmetry that distinguishes fan-out from delegation. It owns its own closed four-value aggregation-policy vocabulary. It owns the cap on how many clones one supervisor process dispatches at a time (see `decision:fanout-parallelism-cap-per-process`), and the per-child producer-candidate handle for producers that also do data processing (see `concept:data-processing`). Fan-out never merges per-partition attribute writebacks onto the parent; an author who needs aggregation routes it through the claim-producer protocol (see `decision:fanout-attribute-merge-rejected`).

Fan-out does not own the dispatch and settlement shape, the child-context closure, or the parent-settlement cascade, which belong to `concept:child-execution`. It does not own state aggregation, which belongs to `concept:node-run`; claim conflict, which belongs to `concept:claim` and `concept:claim-handle`; or execution-context semantics in general, which belong to `concept:run-scope`. See also `concept:claim-tree`, `concept:delegation`, `concept:message`, and `concept:error-policy`.
