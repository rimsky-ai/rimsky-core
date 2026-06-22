---
concept: fan-out
status: as-is
aliases: []
---

# Fan-out

## Definition

Fan-out is an invocation pattern over `concept:child-execution`: a node-level decision to partition a held claim into sub-claims and dispatch one child execution per partition under the **aggregate** settlement mode (see `concept:child-execution`), with an author-chosen **aggregation policy** drawn from `concept:error-policy`. The node holds a parent claim, the producer's partition-split operation takes the parent claim handle plus a partition request and returns N sub-scope descriptors, rimsky opens N sub-claim handles (in the parent-acquisition transaction, per `concept:claim-tree`), and hands the already-acquired sub-claims to the dispatch primitive — one child per partition, each keyed by its partition key, each carrying its sub-claim handle. Settlement aggregates the children's outcomes under the author's policy and settles the parent's claim, per `concept:child-execution`.

Declared in templates on a node spec that names the parent claim, the partition request, the parallelism cap, and the aggregation policy. The aggregation policy is fan-out's only consumer; delegation does not configure or read it.

## Boundaries

Owns: the template surface declaring fan-out, partition cardinality (N is producer-decided via the split), the partition-split mechanics at parent-acquisition, the per-partition sub-claim asymmetry (the genuine asymmetry versus delegation), and the per-child producer-candidate handle for data-processing-capable producers (see `concept:data-processing`). Does NOT own: the dispatch and settlement shape, child-context closure, or the parent-settlement cascade — those belong to `concept:child-execution`; state aggregation (see `concept:node-run`); the aggregation-policy semantics (see `concept:error-policy`); claim conflict (see `concept:claim`, `concept:claim-handle`); execution-context semantics in general (see `concept:run-scope`). Adjacent: `concept:child-execution`, `concept:claim`, `concept:claim-handle`, `concept:claim-tree`, `concept:data-processing`, `concept:node-run`, `concept:message`, `concept:run-scope`, `concept:error-policy`.

## Invariants

- Fan-out requires the named claim be declared on the same node. Missing claim → reject.
- The claim's producer MUST advertise split-scope support in its capabilities. Otherwise template registration rejects.
- The partition-request field is opaque to rimsky's split logic — rimsky does not parse its meaning — but it is resolved through substitution at acquisition, not passed verbatim. Substitution uses the standard resolve context — the same source catalog available to executor-attribute dispatch and to locks-stage substitution per `decision:substitution-grammar-closed`. The partition request is not architecturally distinguished from any other substituted field.
- Sub-claim atomicity per invariant 10: the rimsky-side acquisition transaction inserts the parent claim-handle row AND all sub-claim handle rows AND records all producer-returned addresses, or none of these.
- For data-processing-capable producers, the candidate-begin step fires at sub-claim acquisition (in the same transaction); the producer-candidate handle persists on the sub-claim row and threads into the leaf executor's dispatched request.
- Sub-claim acquisition happens upstream of dispatch: the dispatch primitive receives already-acquired sub-claims and never calls the producer's split (per `concept:child-execution`).
- The producer's split-scope operation is dispatched on a producer-defined partition-request shape and returns a list of sub-scope descriptors, each carrying the substrate-meaningful claim data plus per-partition discriminators. The fan-out node iterates the returned list — uniform across producers; what shapes a given producer accepts is producer-specific.
- A producer's partition-request shape must yield disjoint sub-claim partitions: every emitted sub-scope descriptor names a region of the substrate that does not overlap any sibling's region. Each sub-claim's claim-scope data must distinguish it from every sibling's via the producer's conflict relation (the same relation used to detect cross-claim conflict). A producer asked to emit overlapping partitions must reject the request rather than emit overlapping descriptors.
- Per-partition executor-written attribute writebacks do NOT aggregate onto the fan-out parent's attribute bag. All partitions are runs of the same template node and share the same attribute schema by construction, so flat per-key merging across partitions is structurally guaranteed to collide on every key — there is no non-arbitrary rimsky-side merge. Authors needing per-fan-out aggregation route it through the producer protocol: the base Commit response's `producer_metadata` field surfaces in the parent's writeback (see `decision:wire-commit-response-fields`), and producers advertising the data-processing mix-in aggregate their registered candidates atomically at parent commit (see `concept:data-processing`). Rimsky does not invent an aggregator vocabulary.
