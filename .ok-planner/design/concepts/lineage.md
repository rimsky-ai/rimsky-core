---
concept: lineage
status: as-is
aliases: []
---

# Lineage

## Definition

A persisted projection of computational + data-promotion records. Two record kinds (`leaf_run`, `claim_terminal`); both append-only. The source of truth is the audit log plus the claim-handle lifecycle (see `concept:event-log`, `concept:claim-handle`); the lineage projection is a materialized view rebuildable from those.

The `claim_terminal` record carries a per-record `outcome` discriminator (post-2026-05-16 forensics extension) describing the per-terminal disposition: `committed` (successful Commit), `abandoned` (natural Abandon), `force_cancelled` (sibling-cancel / descendant-cancel walker). The pre-rename `claim_commit` record kind covered only the Commit branch; the rename captures every claim-handle terminal in the same projection.

## Boundaries

Owns: the lineage projection storage, the two record kinds, the operator-facing lineage query surface, the projection-rebuild path (deferred V1). Does NOT own: the source-of-truth audit log (lives in `concept:event-log`), the OpenLineage wire format (lives with the OpenLineage subscriber; see `concept:lineage-record`). Adjacent: `concept:lineage-record`, `concept:event-log`, `concept:claim-handle`, `concept:node-run`.

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

## Notes

Introduced by `spec:2026-05-15-data-platform-extensions-design`. The "materialized projection" framing keeps the lineage surface decoupled from the live runtime; the OpenLineage subscriber polls the projection rather than subscribing to live events.

2026-05-23 — Per `spec:2026-05-23-signal-taxonomy-and-policy-decoupling-design`: the projection's `last_outcome` field on `leaf_run` records is replaced by a `settling_signal_type` field carrying the canonical signal type-path the run settled with (`concept:signal`). Subscribers that decoded `last_outcome` (e.g. the OpenLineage subscriber) read `settling_signal_type` instead; the OpenLineage facet emits the signal type-path. Strictly more expressive than the retired enum.

2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
