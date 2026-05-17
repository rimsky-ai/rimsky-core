---
concept: fan-out
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Fan-out

## Definition

Fan-out is a node-level decision to partition a held claim into sub-claims and dispatch one work unit per sub-claim. Always tied to claim partitioning: the node holds a parent claim, the producer's `SplitScope(parent_claim_handle, partition_request)` returns N sub-scope descriptors, rimsky opens N sub-claim handles (in the parent-acquisition transaction), and dispatches one child leaf run per sub-claim with `child_key = <partition_key>`.

Declared in templates via `fan_out: { claim, partition_request, parallelism?, error_policy }` on the node spec.

## Boundaries

Owns: the per-node fan-out declaration, the `SplitScope` mechanics at parent-acquisition, child leaf-run dispatch keyed by `child_key`, the per-child `producer_candidate_handle` for DataProcessing-capable producers. Does NOT own: state aggregation (see `concept:node-run` state-aggregation table), the `error_policy` semantics (see `concept:node-run`, error-policy alternatives `strict | threshold | best_effort | first`), claim conflict (see `concept:claim`, `concept:claim-handle`). Adjacent: `concept:claim`, `concept:claim-handle`, `concept:data-processing`, `concept:node-run`, `concept:backfill`.

## Invariants

- Fan-out requires the named claim be declared on the same node (via `claims:` or `holds:`). Missing claim → reject.
- The claim's producer MUST advertise `SupportsSplitScope: true` in `Capabilities`. Otherwise template registration rejects.
- The `partition_request` field is opaque bytes passed verbatim to `SplitScope`; rimsky does not parse it. Typically a substitution path (`{{trigger.message.payload.partition_request_override | default: <template-default>}}`) so the same node accepts normal invocations and backfill messages uniformly.
- Sub-claim atomicity per `@blessed-invariant 10`: the rimsky-side acquisition transaction inserts the parent claim_handle row AND all sub-claim handle rows AND records all `Open`-returned addresses, or none of these.
- For DataProcessing-capable producers, `BeginCandidate` fires at sub-claim acquisition (in the same transaction); `producer_candidate_handle` persists on the sub-claim row and threads into the leaf executor's `ExecuteRequest`.
- Each child leaf run gets `parent_run_id = parent's run id`, `child_key = <partition_key>`.

## Annotation sites

- `code:runtime/runner_acquire.go` — parent-acquisition + SplitScope orchestration.
- `code:runtime/runner_subclaim.go` — sub-claim acquisition path.
- `code:foundation/spec/template.go::FanOutSpec` — template DSL row-type.
- `code:graph/attribute/substitution.go::resolveChild` — `{{child.partition_key}}` binding.
- `code:runtime/auto_terminal.go::resolveParentClaimChain` — recursive claim-tree resolution at fan-out parent terminal.
- `code:test/scenarios/run_tree/` — fan-out aggregation scenarios.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The "partition_request is opaque to rimsky" rule is what lets producers expose their own DSL (date ranges, region lists, dynamic queries) without rimsky needing to understand the partitioning domain.
