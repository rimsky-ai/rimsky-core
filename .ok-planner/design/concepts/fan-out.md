---
concept: fan-out
status: as-is
aliases: []
---

# Fan-out

## Definition

Fan-out is an invocation pattern over `concept:child-execution`: a node-level decision to partition a held claim into sub-claims and dispatch one child execution per partition, with an author-specified aggregation policy. The node holds a parent claim, the producer's partition-split operation takes the parent claim handle plus the partition request and returns N sub-scope descriptors, rimsky opens N sub-claim handles (in the parent-acquisition transaction, per `concept:claim-tree`), and hands the already-acquired sub-claims to the dispatch primitive — one child per partition, each keyed by its partition key, each carrying its sub-claim handle. Settlement aggregates the children's outcomes under the author's policy and settles the parent, per `concept:child-execution`.

Declared in templates via `fan_out: { claim, partition_request, parallelism?, error_policy }` on the node spec.

## Boundaries

Owns: the `fan_out:` template surface, partition cardinality (N is producer-decided via the split), the partition-split mechanics at parent-acquisition, the per-partition sub-claim asymmetry (the genuine asymmetry versus delegation), and the per-child producer-candidate handle for data-processing-capable producers (see `concept:data-processing`). Does NOT own: the dispatch and settlement shape, child-context closure, or the parent-settlement cascade — those belong to `concept:child-execution`; state aggregation (see `concept:node-run` state-aggregation table); the `error_policy` semantics (see `concept:node-run`, error-policy alternatives `strict | threshold | best_effort | first`); claim conflict (see `concept:claim`, `concept:claim-handle`); execution-context semantics in general (see `concept:run-scope`). Adjacent: `concept:child-execution`, `concept:claim`, `concept:claim-handle`, `concept:claim-tree`, `concept:data-processing`, `concept:node-run`, `concept:message`, `concept:run-scope`.

## Invariants

- Fan-out requires the named claim be declared on the same node (via `claims:` or `holds:`). Missing claim → reject.
- The claim's producer MUST advertise split-scope support in its capabilities. Otherwise template registration rejects.
- The `partition_request` field is opaque to rimsky's split logic — rimsky does not parse its meaning — but it is resolved through substitution at acquisition, not passed verbatim. Substitution uses the standard resolve context — the same source catalog available to executor-attribute dispatch and to locks-stage substitution per `decision:substitution-grammar-closed`. `partition_request` is not architecturally distinguished from any other substituted field.
- Sub-claim atomicity per `@blessed-invariant 10`: the rimsky-side acquisition transaction inserts the parent claim-handle row AND all sub-claim handle rows AND records all producer-returned addresses, or none of these.
- For data-processing-capable producers, the candidate-begin step fires at sub-claim acquisition (in the same transaction); the producer-candidate handle persists on the sub-claim row and threads into the leaf executor's dispatched request.
- Sub-claim acquisition happens upstream of dispatch: the dispatch primitive receives already-acquired sub-claims and never calls the producer's split (per `concept:child-execution`).
- The producer's SplitScope verb is dispatched on a producer-defined `partition_request` shape and returns a list of SubScopeDescriptors, each carrying the substrate-meaningful claim fields (`claim_scope_data`, `address`, `payload`) plus the per-partition discriminators (`partition_key`, `producer_metadata`). The fan-out node iterates the returned list — uniform across producers; what shapes a given producer accepts is producer-specific.
- A producer's `partition_request` shape must yield disjoint sub-claim partitions: every emitted SubScopeDescriptor names a region of the substrate that does not overlap any sibling's region. Each sub-claim's `claim_scope_data` must distinguish it from every sibling's via the producer's conflict relation (the same relation used to detect cross-claim conflict). A producer that would emit overlapping partitions (e.g. the filesystem store's expand-folder walk asked to enumerate a directory AND its descendants at depth > 1) must reject the request with `InvalidArgument` rather than emit overlapping descriptors.
