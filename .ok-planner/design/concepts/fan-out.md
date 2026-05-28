---
concept: fan-out
status: as-is
aliases: []
---

# Fan-out

## Definition

Fan-out is a node-level decision to partition a held claim into sub-claims and dispatch one work unit per sub-claim. Always tied to claim partitioning: the node holds a parent claim, the producer's partition-split operation takes the parent claim handle plus the partition request and returns N sub-scope descriptors, rimsky opens N sub-claim handles (in the parent-acquisition transaction), and dispatches one child leaf run per sub-claim. Each child runs in its own fan-out partition RunScope (per `concept:run-scope`), with parent-run id = the fan-out parent's run, parent-run-scope id = the fan-out parent's RunScope, and a per-child partition key.

Declared in templates via `fan_out: { claim, partition_request, parallelism?, error_policy }` on the node spec.

## Boundaries

Owns: the per-node fan-out declaration, the partition-split mechanics at parent-acquisition, child leaf-run dispatch keyed by partition key, the per-child producer-candidate handle for data-processing-capable producers (see `concept:data-processing`), the split-driven RunScope creation at parent acquisition. Does NOT own: state aggregation (see `concept:node-run` state-aggregation table), the `error_policy` semantics (see `concept:node-run`, error-policy alternatives `strict | threshold | best_effort | first`), claim conflict (see `concept:claim`, `concept:claim-handle`), RunScope semantics in general (see `concept:run-scope`). Adjacent: `concept:claim`, `concept:claim-handle`, `concept:data-processing`, `concept:node-run`, `concept:backfill`, `concept:run-scope`.

## Invariants

- Fan-out requires the named claim be declared on the same node (via `claims:` or `holds:`). Missing claim → reject.
- The claim's producer MUST advertise split-scope support in its capabilities. Otherwise template registration rejects.
- The `partition_request` field is opaque bytes passed verbatim to the partition-split operation; rimsky does not parse it. Typically a substitution path (`{{trigger.message.payload.partition_request_override | default: <template-default>}}`) so the same node accepts normal invocations and backfill messages uniformly.
- Sub-claim atomicity per `@blessed-invariant 10`: the rimsky-side acquisition transaction inserts the parent claim-handle row AND all sub-claim handle rows AND records all producer-returned addresses, or none of these.
- For data-processing-capable producers, the candidate-begin step fires at sub-claim acquisition (in the same transaction); the producer-candidate handle persists on the sub-claim row and threads into the leaf executor's dispatched request.
- Each child runs in its own fan-out partition RunScope (per `concept:run-scope`): `parent_run_id = fan-out parent's run id`, `parent_run_scope_id = fan-out parent's RunScope id`, `partition_key = <partition_key>`. The child's leaf run lives in this RunScope.

## Notes

Introduced by `spec:2026-05-15-data-platform-extensions`. The "partition_request is opaque to rimsky" rule is what lets producers expose their own DSL (date ranges, region lists, dynamic queries) without rimsky needing to understand the partitioning domain.

2026-05-22 — Reshape per `spec:2026-05-22-fan-out-safety-scope-first`: fan-out children now live in fan-out partition RunScopes (`concept:run-scope`) rather than carrying an inline parent-run id + child key on the run row. The parent-child relationship moves onto the RunScope record via parent-run id + partition key; the child's run row carries only its run-scope id.
- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
