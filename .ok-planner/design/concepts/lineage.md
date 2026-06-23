---
concept: lineage
status: as-is
aliases: []
---

# Lineage

## Definition

A persisted projection of **data lineage** — what each run's output literally depended on, and what each claim-handle resolved to. Two record kinds (`leaf_run`, `claim_terminal`); both append-only. Data dependency is captured via attribute-substitution refs on each leaf-run record (every `{{nodes.X.attribute.Y}}` lookup the executor's attribute template performed becomes a citation to the sender run), and via the claim-tree linkage on each claim-terminal record. The source of truth is the audit log plus the claim-handle lifecycle (see `concept:event-log`, `concept:claim-handle`); the lineage projection is a materialized view rebuildable from those.

The `claim_terminal` record carries a per-record outcome discriminator with three values for the per-terminal disposition. The projection captures every claim-handle terminal in one record kind.

## Boundaries

Owns: the lineage projection storage, the two record kinds, the operator-facing lineage query surface, the projection-rebuild path. Does NOT own: the source-of-truth audit log (lives in `concept:event-log`), the external-receiver wire format (lives with the external-receiver subscriber; see `concept:lineage-record`), **wake-only causality** (the "consumer was woken by upstream's settled signal but its attribute template never read from that upstream" relationship — operators wanting this consult the audit log's signal-emission rows or the wait-set ledger directly). Adjacent: `concept:lineage-record`, `concept:event-log`, `concept:claim-handle`, `concept:node-run`.

## Invariants

- Records are append-only; no UPDATEs.
- Source of truth: the lineage projection is a materialized view rebuildable from the audit log plus the claim-handle lifecycle. The projection writer runs at leaf-run terminal and at claim-handle commit.
- Pass-through nodes — runs whose runtime path never invokes an executor (fan-out parents, which skip executor at the acquire-phase split to dispatch children directly; pure-cascade nodes, which carry no executor declaration and settle on cascade alone) — emit no `leaf_run` record. The projection covers computational units; a pass-through has no computation to cite and no substitution-derived data dependency to capture, so it is structurally absent from the leaf-run surface by design. Causal causality for these runs lives in the audit log's signal-emission and cascade-firing rows, not in lineage.
- Walks are bounded by a configurable depth.

## Query surface

The operator-facing query surface supports point lookups by run id, claim-handle id, source type+id, and producer name, plus recursive backward/forward walks across runs and claim handles bounded by depth.

## Retention

Operator-configurable. Default: retain a lineage record as long as the corresponding artifact (run or claim handle) is retained, plus a configurable trailing window. Manual prune is available.
