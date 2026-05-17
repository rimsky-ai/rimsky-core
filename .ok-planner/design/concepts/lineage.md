---
concept: lineage
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Lineage

## Definition

A projection of computational + data-promotion records, persisted in `rimsky_lineage`. Two record kinds (`leaf_run`, `claim_commit`); both append-only. The source of truth is `rimsky_events` (the audit log) + `rimsky_claim_handles` lifecycle. `rimsky_lineage` is a materialized projection rebuildable from those.

## Boundaries

Owns: the `rimsky_lineage` table, the two record kinds, the control-api `/lineage/...` query surface, the projection-rebuild path (deferred V1). Does NOT own: source-of-truth audit log (lives in `concept:event-log`), the OpenLineage wire format (lives in `subscribers/openlineage/`; see `concept:lineage-record`). Adjacent: `concept:lineage-record`, `concept:event-log`, `concept:claim-handle`, `concept:node-run`.

## Invariants

- Records are append-only; no UPDATEs.
- Source of truth: `rimsky_lineage` is a materialized projection rebuildable from `rimsky_events` + `rimsky_claim_handles`. The projection writer runs at leaf-run terminal and at claim-handle commit.
- Walks are bounded by `depth` parameter (max 50).

## Query surface (control-api)

- `GET /lineage/runs/{run_id}` — single leaf-run record.
- `GET /lineage/runs/{run_id}/ancestors?depth=N` — recursive backward walk (substitution refs + held-claim writers).
- `GET /lineage/runs/{run_id}/descendants?depth=N` — recursive forward walk (downstream readers).
- `GET /lineage/claims/{claim_handle_id}` — single claim-commit record.
- `GET /lineage/claims/{claim_handle_id}/ancestors?depth=N` — backward through sub-claim manifest and the runs that wrote each sub-claim.
- `GET /lineage/by-source/{source_type}/{source_id}` — reverse lookup.
- `GET /lineage/by-producer/{executor_name}?version=...` — by-producer.

## Annotation sites

- `code:foundation/persistence/lineage.go` — table-shape interface.
- `code:runtime/lineage.go` — `WriteLeafRunLineage` + `WriteClaimCommitLineage`.
- `code:control/controlapi/lineage.go` — query handlers.
- `code:control/cli/lineage.go` — `rimsky-cli lineage` subcommand group.
- `code:subscribers/openlineage/` — emitter consuming the projection.

## Retention

Operator-configurable. Default: retain as long as the corresponding artifact (run or claim handle) is retained, plus a trailing window (`retention.lineage_trailing: 30d`). Manual prune via `rimsky-cli lineage prune`.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The "materialized projection" framing keeps the lineage surface decoupled from the live runtime; the openlineage subscriber polls the projection rather than subscribing to live events.
