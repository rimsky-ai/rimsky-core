---
concept: lineage-record
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Lineage record

## Definition

An append-only record in `rimsky_lineage`. Two kinds:

- **`leaf_run`** — one per leaf-run terminal. Captures the computational unit: run_id, node alias, child_key, parent_run_id, frame trigger metadata, substitution refs, held claims, executor + template metadata, terminal kind and last_outcome.
- **`claim_terminal`** — one per claim-handle terminal (Commit, natural Abandon, force-cancelled Abandon). Captures the data-promotion unit: claim_handle_id, version_id (when DataProcessing-capable), producer name, claim_scope_data_hash, parent_run_id, frame_id, sub_claim_handle_ids (for fan-out parents), committed_at, outcome, cause. Pre-2026-05-16 this was `claim_commit` and covered only Commit; the rename extends the projection to every claim-handle terminal so post-mortem queries can reconstruct natural-vs-force-cancelled Abandon flows alongside Commits.

## Boundaries

Owns: the per-kind record shape, the projection-write path. Does NOT own: the table or query surface (lives in `concept:lineage`), the OpenLineage emission (lives in `subscribers/openlineage/`). Adjacent: `concept:lineage`, `concept:claim-handle`, `concept:node-run`, `concept:auto-terminal`.

## Invariants

- Both kinds are append-only; no UPDATEs.
- `leaf_run` records emit at the leaf-run terminal path (`runtime/lineage_writer.go::WriteLeafRunLineage`, fired from the per-terminal handlers in `runtime/runner_terminal*.go`).
- `claim_terminal` records emit at the unified terminal-decision engine's forensics emit site (`runtime/lineage_writer.go::WriteClaimTerminalLineage`, fired from `runtime/terminal_decision_forensics.go::emitTerminalForensics`) so every Commit / Abandon / force-cancelled resolution lands a row in the same shape.
- Outcome is REQUIRED on `claim_terminal` rows. The writer rejects an empty Outcome — an Abandon path that forgets to set it cannot silently produce a row marked `committed`.
- All fields are scalars (no payload bytes); held-claim references carry `claim_scope_data_hash` (SHA-256 over `claim_scope_data` bytes), not the bytes themselves. Per `@blessed-invariant 20/21` the inert bytes don't appear in lineage records.

## Leaf-run record shape

```
{
  record_kind: "leaf_run",
  instance_id, frame_id, observed_at, record: {
    run_id, node_id, frame_id, child_key,
    node_alias, parent_run_id,
    frame_trigger_kind, trigger_message_id,
    substitution_refs: [
      {source_kind, source_node_alias, source_version_or_id}
    ],
    held_claims: [{claim_handle_id, role, producer_name, claim_scope_data_hash}],
    executor_name, executor_version,
    template_hash, template_node_alias,
    params_snapshot_hash, userdata_hash, claim_scope_data_hash,
    state, last_outcome, changed, terminal_kind,
    error_class, extra
  }
}
```

### Substitution-ref entries

Post-2026-05-17 (cycle 6) the `substitution_refs` slice carries the
richer object shape `{source_kind, source_node_alias,
source_version_or_id}`. Two `source_kind` values are emitted by the
runtime writer:

- `attribute` / `event` — one entry per `{{nodes.X.attribute.Y}}` /
  `{{nodes.X.event.Y}}` directive parsed from the receiver's
  `attributes.schema`. `source_node_alias` is the upstream node-type
  named in the directive; `source_version_or_id` is the attribute /
  event name. These are informational; the ancestor walker skips them
  because the `source_version_or_id` isn't a UUID.
- `run` — one entry per distinct upstream sender, keyed by the
  upstream node's most recent leaf-run row's `run_id` (looked up in
  the lineage projection at emit time). `source_version_or_id` is a
  UUID. The ancestor walker
  (`code:control/controlapi/lineage.go::extractSubstitutionRefRunIDs`)
  reads these and follows the link, so
  `route:GET /lineage/runs/{run_id}/ancestors` returns the actual
  upstream chain rather than the empty set the pre-cycle-6 build
  produced.

The pre-cycle-6 build emitted `substitution_refs` as a bare
`[]string` of attribute names with no upstream-run linkage; the
ancestor walker had a dead `[]string` fallback decode branch that
never fired in practice because no writer populated the field at
all. Both the legacy `[]string` shape and the fallback decode have
been removed.

### Terminal kinds

The `terminal_kind` field on `leaf_run` rows discriminates the emission site. The value set is closed; each value pairs with a documented emit site in `runtime/`:

- **`complete`** — leaf executor reported terminal-complete; the standard success path. Emitted by `code:runtime/runner_terminal.go::applyTerminalComplete`. Row state is `fresh`; `settling_signal_type` carries `terminal/success` (the executor's `changed` flag rides on the signal payload, not on the row column — receiver-side selectivity uses CEL `when: payload.changed`).
- **`park`** — leaf executor entered park (via `Park` terminal). Emitted by `code:runtime/runner_terminal_park.go::applyTerminalPark`. Row state is `parked`; `settling_signal_type` carries `terminal/park/snooze` or `terminal/park/await_callback` per `ParkReason`.
- **`errored`** — leaf executor reported terminal-error (or the `Blocked` terminal collapsed onto `Error{error_class: "executor_blocked"}`). Emitted by `code:runtime/runner_error_policy.go::applyErrorPolicy` with row state `failed` + `settling_signal_type=terminal/error/<class>` on the `give_up` branch. Also emitted (with the same `terminal_kind: "errored"`) on the `pass` branch of `applyResolvedAction` — that row carries the same `terminal_kind: "errored"` paired with row state `fresh` + `settling_signal_type=terminal/error/<class>` (the signal type-path is identical to give_up; the disposition discriminator is the `Resolution.Color` axis, surfaced via row state). Consumers reconstructing "what the executor reported" read the signal type-path; consumers tracking the resolved disposition read row state.
- **`subgraph_call`** — sub-graph caller's internal-cascade-fire emission. Emitted by `code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller` at the moment the absorbed entry terminal fires and the sub-graph's non-entry internal nodes dispatch as children. Row state is `running` (the parent run stays running through the internal cascade) and `params_snapshot_hash` / `attributes_hash` / `parent_run_id` reflect the calling node's inputs at internal-cascade-fire time — not the post-aggregation outcome. See "Sub-graph caller emission" below for the two-row shape.

The set is deliberately small at pre-v1; if a new emit site lands, add the value here and to the OpenLineage subscriber's facet documentation in lockstep. (Pre-dispatch acquisition failure resolved via `error_types: { "acquire/unavailable": { policy: [pass] } }` does NOT yet emit a leaf_run row; the resolution happens before the run enters `running` and there is no run-row yet to anchor the lineage record. If that gap closes pre-v1, it lands as `terminal_kind: "acquire_pass"` or similar.)

### Sub-graph caller emission

Sub-graph callers produce **two** `leaf_run` rows per dispatch, both keyed to the same calling-run UUID:

1. The first row fires from `code:runtime/subgraph_dispatch.go::applyTerminalCompleteSubgraphCaller` at internal-cascade-fire time. `terminal_kind: "subgraph_call"`, `state: "running"`. Captures the calling node's inputs (held claims, params, userdata) as the absorbed entry terminal — the "what the caller saw" moment.
2. The second row fires from `code:runtime/runner_terminal.go::applyTerminalComplete` later, when the parent run's aggregation terminal lands (driven by the last internal child's terminal via `code:runtime/state_propagation.go::PropagateFromChildState`). `terminal_kind: "complete"`, `state: "fresh"`. Captures the post-aggregation outcome.

Downstream consumers pair the two rows by `run_id` and discriminate on `terminal_kind`. The OpenLineage subscriber (`code:subscribers/openlineage/emitter.go::MakeLeafRunEvent`) maps every leaf_run row to a `COMPLETE` event, so a sub-graph caller produces TWO `COMPLETE` events at the same `runId` — discriminated by `rimsky.terminal_kind` in the rimsky facet. This is intentional (the calling node's inputs are semantically distinct from the post-aggregation outcome); it is not a duplicate emission. Backends that treat `COMPLETE` as a terminal-state signal should branch on `rimsky.terminal_kind`.

## Claim-terminal record shape

```
{
  record_kind: "claim_terminal",
  outcome: "committed" | "abandoned" | "force_cancelled",
  instance_id, frame_id, observed_at, record: {
    claim_handle_id, run_id, node_id, frame_id,
    parent_claim_handle_id, parent_run_id,
    sub_claim_handle_ids: [...],
    producer_name, claim_scope_data_hash, version_id,
    outcome, cause,                       # "natural" | "sibling_cancel" | "descendant_cancel"
    committed_at,
    producer_metadata
  }
}
```

The per-row `outcome` column on `rimsky_lineage` mirrors the JSON `outcome` field so analytical queries can filter without JSON extraction. The three-value discriminator (`committed` / `abandoned` / `force_cancelled`) distinguishes the per-terminal disposition; the `cause` field further discriminates Abandon provenance — `natural` (give_up / error policy), `sibling_cancel` (`strict.cancel_siblings: true` walker), `descendant_cancel` (parent-Abandon recursive descent).

## Annotation sites

- `code:foundation/persistence/lineage.go::LineageTable` — table interface.
- `code:runtime/lineage_writer.go::WriteLeafRunLineage` / `::WriteClaimTerminalLineage` — write paths.
- `code:runtime/terminal_decision_forensics.go::emitTerminalForensics` — single emit site for `claim_terminal` rows.
- `code:subscribers/openlineage/subscriber.go::LeafRunRecord` / `::ClaimTerminalRecord` — consumer-side Go shapes.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`; renamed and extended on 2026-05-16 (forensics extension) so the projection covers every claim-handle terminal rather than only Commits. The two-kind decomposition mirrors OpenLineage's run-vs-dataset event split, so the subscriber's mapping (`subscribers/openlineage/emitter.go::MakeLeafRunEvent` / `::MakeClaimTerminalEvent`) is a thin transformation rather than a re-projection.

2026-05-22 — Updated for ClaimScope rename and run-tree reshape per spec `.ok-planner/specs/2026-05-22-fan-out-safety-scope-first-design.md`. `scope_data_hash` references renamed to `claim_scope_data_hash` (reflecting the underlying column rename `scope_data` → `claim_scope_data`). The lineage JSON's `parent_run_id` and `child_key` fields on `leaf_run` rows are preserved for back-compat with existing forensic queries, but their source changes: rather than reading from now-dropped inline columns on `rimsky_node_runs`, the writer joins through `rimsky_node_runs.run_scope_id → rimsky_run_scopes` and reads `parent_run_id` and `partition_key` from there (`partition_key` projects as `child_key` in the lineage JSON for back-compat).

2026-05-23 — Per spec `.ok-planner/specs/2026-05-23-signal-taxonomy-and-policy-decoupling-design.md`: lineage rows replace the `last_outcome` projection with a `settling_signal_type` field carrying the canonical signal type-path of the settling resolution (`terminal/success`, `terminal/error/<class>`, `terminal/park/<reason>`, `terminal/infra/<reason>`). The new field is strictly more expressive than `last_outcome` and aligns with `concept:signal`'s canonical taxonomy. The rename happens inside the JSONB `record` column — not a top-level schema migration.
