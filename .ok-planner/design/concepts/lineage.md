---
concept: lineage
status: as-is
aliases: []
---

# Lineage

## Definition

A persisted projection of computational + data-promotion records. Two record kinds (`leaf_run`, `claim_terminal`); both append-only. The source of truth is the audit log plus the claim-handle lifecycle (see `concept:event-log`, `concept:claim-handle`); the lineage projection is a materialized view rebuildable from those.

The `claim_terminal` record carries a per-record `outcome` discriminator describing the per-terminal disposition: `committed` (successful Commit), `abandoned` (natural Abandon), `force_cancelled` (sibling-cancel / descendant-cancel walker). The projection captures every claim-handle terminal in one record kind.

## Boundaries

Owns: the lineage projection storage, the two record kinds, the operator-facing lineage query surface, the projection-rebuild path. Does NOT own: the source-of-truth audit log (lives in `concept:event-log`), the OpenLineage wire format (lives with the OpenLineage subscriber; see `concept:lineage-record`). Adjacent: `concept:lineage-record`, `concept:event-log`, `concept:claim-handle`, `concept:node-run`.

## Invariants

- Records are append-only; no UPDATEs.
- Source of truth: the lineage projection is a materialized view rebuildable from the audit log plus the claim-handle lifecycle. The projection writer runs at leaf-run terminal and at claim-handle commit.
- Walks are bounded by a `depth` parameter (max 50).

## Query surface

The operator-facing query surface returns:

- A single leaf-run record by run id.
- A recursive backward ancestor walk from a run (following substitution refs + held-claim writers), bounded by depth.
- A recursive forward descendant walk from a run (downstream readers), bounded by depth.
- A single claim-terminal record by claim-handle id.
- A backward ancestor walk from a claim handle, through the sub-claim manifest and the runs that wrote each sub-claim, bounded by depth.
- A reverse lookup by source type + source id.
- A lookup by producer name (optionally pinned to a version).

## Retention

Operator-configurable. Default: retain a lineage record as long as the corresponding artifact (run or claim handle) is retained, plus a configurable trailing window. Manual prune is available via the operator CLI.
