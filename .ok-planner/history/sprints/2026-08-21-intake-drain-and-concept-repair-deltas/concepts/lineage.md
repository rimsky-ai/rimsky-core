---
concept: lineage
---

# Lineage

## What it is

Lineage is a persisted projection of data lineage: what each run's output depended on, and what each claim handle resolved to. It holds two append-only record kinds, the leaf-run record and the claim-terminal record (see `concept:lineage-record`). Data dependency reaches the projection two ways: on a leaf-run record, every attribute-substitution reference the node declares becomes a citation to the sender's most recent run; on a claim-terminal record, the claim-tree linkage carries it. A claim-terminal record carries a per-terminal disposition with three values, and the projection captures every claim-handle terminal. The audit log and the claim-handle lifecycle are the source of truth (see `concept:event-log`, `concept:claim-handle`); rimsky writes the projection forward from them at terminal time.

## Purpose

Lineage lets an operator ask what a value depended on and how a promotion resolved without reading the audit log back. Because rimsky writes the projection forward at terminal time, the answer is a point lookup and a bounded walk rather than a reconstruction.

## Boundaries

Lineage owns the projection's storage, its two record kinds, and the operator-facing query surface over it. It does not own the audit log that is its source of truth (see `concept:event-log`), the wire format an external receiver consumes (see `concept:lineage-record`), or **wake-only causality** — the relationship where an upstream's settled signal woke a consumer whose attribute template never read from that upstream. An operator asking that question reads the audit log's signal-emission entries or the wait-set ledger instead.

Runs that invoke no executor write no record (see `decision:lineage-records-computation-only`). Causality for such a run lives in the audit log's signal-emission and cascade-firing entries.

see also: `concept:lineage-record`, `concept:event-log`, `concept:claim-handle`, `concept:node-run`, `concept:wait-set`.
