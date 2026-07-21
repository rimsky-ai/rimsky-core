---
concept: claim-lifetime
status: as-is
aliases: []
---

# Claim lifetime

## Definition

A per-claim property selecting subgraph or durable lifetime. Governs auto-terminal behavior:

- **subgraph** (default) — auto-terminal fires Commit (all-success) or Abandon (any-failed) at holding-subgraph completion; the claim handle row is **promoted** to a committed (or abandoned) state and preserved for forensics. The retention sweep reaps the row after the configured trailing window elapses.
- **durable** — auto-terminal still fires Commit (or Abandon); on success, the claim handle row is promoted to a committed state and is **exempt from the retention sweep** (asset surface). The handle is available for future dispatches to co-hold and for asset-presentation queries. Released only by explicit operator action (the asset-delete endpoint) or instance termination (`ReleaseCommittedDurableClaims`, which releases every committed-durable handle of the instance, held or not); Release goes through the absence-guarded resolved-row delete path (no promotion → already non-active when Release fires).

## Boundaries

Owns: the per-claim lifetime selector surfaced in the template, the lifetime field on the claim-handle ledger, the auto-terminal skip rule for non-active rows (so committed-durable rows survive past promotion), the retention sweep's exemption for committed-durable rows, the orphan-claim reaper's skip rule for non-active rows. Does NOT own: the asset presentation surface (see `concept:asset`), the DataProcessing protocol (see `concept:data-processing`). Adjacent: `concept:claim`, `concept:claim-handle`, `concept:asset`, `concept:auto-terminal`.

## Invariants

- `lifetime: durable` requires the claim's producer advertise data-processing capability for the claim to qualify as an asset. A `durable` claim against a non-DataProcessing producer is still durable (the row persists), just not surfaced as an asset.
- Held-durable claim handles persist across instance dispatches. Auto-terminal commit on a `lifetime: durable` claim promotes the row to a committed state; the retention sweep skips committed-durable rows so they live until explicit Release.
- The orphan-claim reaper skips all non-active rows; the expiry timestamp is meaningful only for active rows.
- The recursive parent-claim resolver treats any non-active child (committed-durable, committed-subgraph, abandoned) the same as a resolved-and-released child: it doesn't block the parent's auto-terminal.
- Conflict detection includes committed-durable rows (the producer still occupies the scope until Release); committed-subgraph rows do NOT participate (Commit already ended the producer's occupation of the scope).
