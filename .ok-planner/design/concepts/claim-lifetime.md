---
concept: claim-lifetime
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Claim lifetime

## Definition

Per-claim property in the `claims:` block: `lifetime: subgraph | durable` (default `subgraph`). Governs auto-terminal behavior:

- **`subgraph`** (default) — auto-terminal fires `Commit` (all-success) or `Abandon` (any-failed) at holding-subgraph completion; the claim handle row is **promoted** to `state = 'committed'` (or `'abandoned'`) and preserved for forensics. The retention sweep (`SweepClaimHandleRetention`) reaps the row after `retention.claim_handles_trailing` elapses (default 30d).
- **`durable`** — auto-terminal still fires `Commit` (or `Abandon`); on success, the claim handle row is promoted to `state = 'committed'` and is **exempt from the retention sweep** (asset surface). The handle is available for future dispatches to co-hold via `holds:` and for asset-presentation queries. Released only by explicit operator action (`DELETE /instances/{id}/assets/{alias}`) or instance termination (`runtime/instance_termination.go::ReleaseHeldDurableClaims`); Release goes through the absence-guarded `DeleteResolved` path (no Promote → already non-active when Release fires).

## Boundaries

Owns: the `lifetime:` template field, the `lifetime` column on `rimsky_claim_handles`, the auto-terminal skip rule for `state != 'active'` rows (so committed-durable rows survive past Promote), the retention sweep's exemption for `state = 'committed' AND lifetime = 'durable'` rows, the orphan-claim reaper's skip rule for non-active rows. Does NOT own: the asset presentation surface (see `concept:asset`), the DataProcessing protocol (see `concept:data-processing`). Adjacent: `concept:claim`, `concept:claim-handle`, `concept:asset`, `concept:auto-terminal`.

## Invariants

- `lifetime: durable` requires the claim's producer advertise `data_processing` in `Capabilities.Protocols` for the claim to qualify as an asset. A `durable` claim against a non-DataProcessing producer is still durable (the row persists), just not surfaced as an asset.
- Held-durable claim handles persist across instance dispatches (`@blessed-invariant 22`, refreshed post-2026-05-17). Auto-terminal Commit on a `lifetime: durable` claim promotes the row to `state = 'committed'`; the retention sweep skips committed-durable rows so they live until explicit Release.
- The orphan-claim reaper skips all non-`active` rows; `expires_at` is meaningful only for active rows.
- The recursive parent-claim resolver in `runtime/auto_terminal.go::resolveParentClaimChain` treats any non-active child (committed-durable, committed-subgraph, abandoned) the same as a resolved-and-released child: it doesn't block the parent's auto-terminal.
- `ListByProducerScope` includes committed-durable rows in conflict detection (the producer still occupies the scope until Release); committed-subgraph rows do NOT participate (the producer Released the scope at Commit).

## Annotation sites

- `code:foundation/spec/graphs.go::ClaimLifetimeDurable` — the lifetime constant.
- `code:foundation/spec/template.go` — `ClaimSpec.Lifetime` field on the claim spec.
- `code:foundation/persistence/claim_handles.go::ClaimHandleRow.Lifetime` — the persisted column.
- `code:foundation/persistence/postgres/claim_handles.go::Promote` — the active→committed/abandoned state transition.
- `code:runtime/auto_terminal.go::CheckAndFireResolution` — auto-terminal honors the `lifetime` setting (skips non-active rows; the resolveParentClaimChain walk treats non-active children as resolved).
- `code:runtime/sweep_claim_handle_retention.go::SweepClaimHandleRetention` — retention reaper that excludes committed-durable rows.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md` as the core enabler of the asset pattern. The default `subgraph` lifetime preserves the existing held-claim semantics (auto-terminal Promotes to committed; retention sweep reaps at cutoff); `durable` opts into asset semantics (retention sweep skips; only Release reaps).

State-column refactor per `spec:2026-05-17-post-data-platform-cleanup`: replaced `SetHeldDurable(held_durable=true)` with `Promote(committed)` plus a lifetime-aware retention sweep. The terminal-decision engine is now uniform (one Promote path for both lifetimes); the asset-vs-not distinction lives entirely in the row's `lifetime` column.
