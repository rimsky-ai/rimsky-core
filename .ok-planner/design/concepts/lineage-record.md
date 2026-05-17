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
- **`claim_commit`** — one per `Commit` of a claim handle (DataProcessing or not). Captures the data-promotion unit: claim_handle_id, version_id (when DataProcessing-capable), producer name, scope_data_hash, parent_run_id, frame_id, sub_claim_handle_ids (for fan-out parents), committed_at.

## Boundaries

Owns: the per-kind record shape, the projection-write path. Does NOT own: the table or query surface (lives in `concept:lineage`), the OpenLineage emission (lives in `subscribers/openlineage/`). Adjacent: `concept:lineage`, `concept:claim-handle`, `concept:node-run`, `concept:auto-terminal`.

## Invariants

- Both kinds are append-only; no UPDATEs.
- `leaf_run` records emit at the leaf-run terminal path (`runtime/runner_terminal.go::WriteLeafRunLineage`).
- `claim_commit` records emit at the auto-terminal Commit path (`runtime/auto_terminal.go::WriteClaimCommitLineage`).
- All fields are scalars (no payload bytes); held-claim references carry `scope_data_hash` (SHA-256 over `scope_data` bytes), not the bytes themselves. Per `@blessed-invariant 20/21` the inert bytes don't appear in lineage records.

## Leaf-run record shape

```
{
  record_kind: "leaf_run",
  instance_id, frame_id, observed_at, record: {
    run_id, node_alias, child_key, parent_run_id,
    frame_trigger_kind, trigger_message_id,
    substitution_refs: [...],
    held_claims: [{claim_handle_id, role, producer_name, scope_data_hash}],
    executor_name, executor_version,
    template_hash, template_node_alias,
    params_snapshot_hash, userdata_hash,
    changed, last_outcome, terminal_kind
  }
}
```

## Claim-commit record shape

```
{
  record_kind: "claim_commit",
  instance_id, frame_id, observed_at, record: {
    claim_handle_id, version_id,
    producer_name, scope_data_hash,
    parent_run_id, frame_id,
    sub_claim_handle_ids: [...],
    committed_at
  }
}
```

## Annotation sites

- `code:foundation/persistence/lineage.go::LineageTable` — table interface.
- `code:runtime/lineage.go::WriteLeafRunLineage` / `::WriteClaimCommitLineage` — write paths.
- `code:subscribers/openlineage/subscriber.go::LeafRunRecord` / `::ClaimCommitRecord` — consumer-side Go shapes.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The two-kind decomposition mirrors OpenLineage's run-vs-dataset event split, so the subscriber's mapping (`subscribers/openlineage/emitter.go::MakeLeafRunEvent` / `::MakeClaimCommitEvent`) is a thin transformation rather than a re-projection.
