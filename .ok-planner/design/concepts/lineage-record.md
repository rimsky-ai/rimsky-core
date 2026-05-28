---
concept: lineage-record
status: as-is
aliases: []
---

# Lineage record

## Definition

An append-only record in the lineage projection (see `concept:lineage`). Two kinds:

- **`leaf_run`** — one per leaf-run terminal. Captures the computational unit: run_id, node alias, child_key, parent_run_id, frame trigger metadata, substitution refs, held claims, executor + template metadata, terminal kind and last_outcome.
- **`claim_terminal`** — one per claim-handle terminal (commit, natural abandon, force-cancelled abandon). Captures the data-promotion unit: claim_handle_id, version_id (when data-processing-capable), producer name, claim_scope_data_hash, parent_run_id, frame_id, sub_claim_handle_ids (for fan-out parents), committed_at, outcome, cause. Pre-2026-05-16 this was `claim_commit` and covered only commits; the rename extends the projection to every claim-handle terminal so post-mortem queries can reconstruct natural-vs-force-cancelled abandon flows alongside commits.

## Boundaries

Owns: the per-kind record shape, the projection-write path. Does NOT own: the projection storage or query surface (lives in `concept:lineage`), the OpenLineage emission (lives with the OpenLineage subscriber). Adjacent: `concept:lineage`, `concept:claim-handle`, `concept:node-run`, `concept:auto-terminal`.

## Invariants

- Both kinds are append-only; no UPDATEs.
- `leaf_run` records emit at the leaf-run terminal path, fired from the per-terminal handlers in the runtime.
- `claim_terminal` records emit at the unified terminal-decision engine's forensics emit site, so every commit / abandon / force-cancelled resolution lands a record in the same shape.
- An outcome value is REQUIRED on `claim_terminal` records. The writer rejects an empty outcome — an abandon path that forgets to set it cannot silently produce a record marked `committed`.
- All fields are scalars (no payload bytes); held-claim references carry the claim-scope-data hash (SHA-256 over the claim-scope-data bytes), not the bytes themselves. Per `@blessed-invariant 20/21` the inert bytes don't appear in lineage records.

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
  upstream node's most recent leaf-run record's `run_id` (looked up in
  the lineage projection at emit time). `source_version_or_id` is a
  UUID. The ancestor walker reads these and follows the link, so the
  run-ancestor query returns the actual upstream chain rather than the
  empty set the pre-cycle-6 build produced.

The pre-cycle-6 build emitted `substitution_refs` as a bare
list of attribute-name strings with no upstream-run linkage; the
ancestor walker had a dead fallback decode branch for that
string-list shape that never fired in practice because no writer
populated the field at all. Both the legacy string-list shape and
the fallback decode have been removed.

### Terminal kinds

The `terminal_kind` field on `leaf_run` records discriminates the emission site. The value set is closed; each value pairs with a documented emit site in the runtime:

- **`complete`** — leaf executor reported terminal-complete; the standard success path. Emitted by the terminal-complete handler. Record state is `fresh`; `settling_signal_type` carries `terminal/success` (the executor's `changed` flag rides on the signal payload, not on the record's state column — receiver-side selectivity uses the CEL `when: payload.changed` predicate).
- **`park`** — leaf executor entered park (via the park terminal). Emitted by the terminal-park handler. Record state is `parked`; `settling_signal_type` carries `terminal/park/snooze` or `terminal/park/await_callback` per the park reason.
- **`errored`** — leaf executor reported terminal-error (or the blocked terminal collapsed onto an `executor_blocked` error class). Emitted by the error-policy handler with record state `failed` + `settling_signal_type=terminal/error/<class>` on the `give_up` branch. Also emitted (with the same `terminal_kind: "errored"`) on the `pass` resolved-action branch — that record carries the same `terminal_kind: "errored"` paired with record state `fresh` + `settling_signal_type=terminal/error/<class>` (the signal type-path is identical to give_up; the disposition discriminator is the resolution-color axis, surfaced via record state). Consumers reconstructing "what the executor reported" read the signal type-path; consumers tracking the resolved disposition read record state.
- **`subgraph_call`** — sub-graph caller's internal-cascade-fire emission. Emitted by the sub-graph-caller terminal-complete handler at the moment the absorbed entry terminal fires and the sub-graph's non-entry internal nodes dispatch as children. Record state is `running` (the parent run stays running through the internal cascade) and `params_snapshot_hash` / `attributes_hash` / `parent_run_id` reflect the calling node's inputs at internal-cascade-fire time — not the post-aggregation outcome. See "Sub-graph caller emission" below for the two-record shape.

The set is deliberately small at pre-v1; if a new emit site lands, add the value here and to the OpenLineage facet schema in lockstep. (A pre-dispatch acquisition failure resolved via a `pass` error-type policy does NOT yet emit a leaf_run record; the resolution happens before the run enters `running` and there is no run-row yet to anchor the lineage record. If that gap closes pre-v1, it lands as `terminal_kind: "acquire_pass"` or similar.)

### Sub-graph caller emission

Sub-graph callers produce **two** `leaf_run` records per dispatch, both keyed to the same calling-run UUID:

1. The first record fires from the sub-graph-caller terminal-complete handler at internal-cascade-fire time. `terminal_kind: "subgraph_call"`, `state: "running"`. Captures the calling node's inputs (held claims, params, userdata) as the absorbed entry terminal — the "what the caller saw" moment.
2. The second record fires from the terminal-complete handler later, when the parent run's aggregation terminal lands (driven by the last internal child's terminal via state propagation). `terminal_kind: "complete"`, `state: "fresh"`. Captures the post-aggregation outcome.

Downstream consumers pair the two records by `run_id` and discriminate on `terminal_kind`. The OpenLineage subscriber maps every leaf_run record to a `COMPLETE` event, so a sub-graph caller produces TWO `COMPLETE` events at the same `runId` — discriminated by `rimsky.terminal_kind` in the rimsky facet. This is intentional (the calling node's inputs are semantically distinct from the post-aggregation outcome); it is not a duplicate emission. Backends that treat `COMPLETE` as a terminal-state signal should branch on `rimsky.terminal_kind`.

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

The per-record `outcome` discriminator mirrors the JSON `outcome` field so analytical queries can filter without JSON extraction. The three-value discriminator (`committed` / `abandoned` / `force_cancelled`) distinguishes the per-terminal disposition; the `cause` field further discriminates Abandon provenance — `natural` (give_up / error policy), `sibling_cancel` (sibling-cancel walker), `descendant_cancel` (parent-Abandon recursive descent).

## Notes

Introduced by `spec:2026-05-15-data-platform-extensions-design`; renamed and extended on 2026-05-16 (forensics extension) so the projection covers every claim-handle terminal rather than only Commits. The two-kind decomposition mirrors OpenLineage's run-vs-dataset event split, so the subscriber's mapping is a thin transformation rather than a re-projection.

2026-05-22 — Updated for the claim-scope rename and run-tree reshape per `spec:2026-05-22-fan-out-safety-scope-first-design`. Scope-data-hash references renamed to claim-scope-data-hash (reflecting the underlying rename of the scope-data field to claim-scope-data). The lineage JSON's `parent_run_id` and `child_key` fields on `leaf_run` records are preserved for back-compat with existing forensic queries, but their source changes: rather than reading from now-dropped inline columns on the node-run row, the writer joins through the node-run's run-scope reference (per `concept:run-scope`) and reads `parent_run_id` and `partition_key` from there (`partition_key` projects as `child_key` in the lineage JSON for back-compat).

2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.

2026-05-23 — Per `spec:2026-05-23-signal-taxonomy-and-policy-decoupling`: lineage rows replace the `last_outcome` projection with a `settling_signal_type` field carrying the canonical signal type-path of the settling resolution (`terminal/success`, `terminal/error/<class>`, `terminal/park/<reason>`, `terminal/infra/<reason>`). The new field is strictly more expressive than `last_outcome` and aligns with `concept:signal`'s canonical taxonomy. The rename happens inside the JSONB `record` column — not a top-level schema migration.
