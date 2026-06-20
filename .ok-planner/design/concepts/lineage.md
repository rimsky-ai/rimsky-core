---
concept: lineage
status: as-is
aliases: []
---

# Lineage

## Definition

A persisted projection of computational + data-promotion records. Two record kinds (`leaf_run`, `claim_terminal`); both append-only. The source of truth is the audit log plus the claim-handle lifecycle (see `concept:event-log`, `concept:claim-handle`); the lineage projection is a materialized view rebuildable from those.

The `claim_terminal` record carries a per-record outcome discriminator with three values for the per-terminal disposition. The projection captures every claim-handle terminal in one record kind.

## Boundaries

Owns: the lineage projection storage, the two record kinds, the operator-facing lineage query surface, the projection-rebuild path. Does NOT own: the source-of-truth audit log (lives in `concept:event-log`), the external-receiver wire format (lives with the external-receiver subscriber; see `concept:lineage-record`). Adjacent: `concept:lineage-record`, `concept:event-log`, `concept:claim-handle`, `concept:node-run`.

## Invariants

- Records are append-only; no UPDATEs.
- Source of truth: the lineage projection is a materialized view rebuildable from the audit log plus the claim-handle lifecycle. The projection writer runs at leaf-run terminal and at claim-handle commit.
- Walks are bounded by a configurable depth.

## Query surface

The operator-facing query surface supports point lookups by run id, claim-handle id, source type+id, and producer name, plus recursive backward/forward walks across runs and claim handles bounded by depth.

## Retention

Operator-configurable. Default: retain a lineage record as long as the corresponding artifact (run or claim handle) is retained, plus a configurable trailing window. Manual prune is available.
