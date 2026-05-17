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

- **`subgraph`** (default) — auto-terminal fires `Commit` (all-success) or `Abandon` (any-failed) at holding-subgraph completion; the claim handle row is deleted.
- **`durable`** — auto-terminal still fires `Commit` (or `Abandon`); on success, the claim handle row persists with `held_durable: true`. The handle is available for future dispatches to co-hold via `holds:` and for asset-presentation queries. Released only by explicit operator action (`DELETE /instances/{id}/assets/{alias}`) or instance termination (`runtime/instance_termination.go::ReleaseHeldDurableClaims`).

## Boundaries

Owns: the `lifetime:` template field, the `held_durable` column on `rimsky_claim_handles`, the conditional skip in auto-terminal for `held_durable = true` rows, the orphan-claim reaper's skip rule for held-durable. Does NOT own: the asset presentation surface (see `concept:asset`), the DataProcessing protocol (see `concept:data-processing`). Adjacent: `concept:claim`, `concept:claim-handle`, `concept:asset`, `concept:auto-terminal`.

## Invariants

- `lifetime: durable` requires the claim's producer advertise `data_processing` in `Capabilities.Protocols` for the claim to qualify as an asset. A `durable` claim against a non-DataProcessing producer is still durable (the row persists), just not surfaced as an asset.
- Held-durable claim handles persist across instance dispatches (`@blessed-invariant 22`). Auto-terminal Commit on a `lifetime: durable` claim flips `held_durable = true` instead of deleting the row.
- The orphan-claim reaper skips `held_durable = true` rows; the row's `expires_at` doesn't apply once durable is set.
- The recursive parent-claim resolver in `runtime/auto_terminal.go::resolveParentClaimChain` treats a held-durable child the same as a deleted child: it doesn't block the parent's auto-terminal.

## Annotation sites

- `code:foundation/spec/graphs.go::ClaimLifetimeDurable` — the lifetime constant.
- `code:foundation/spec/template.go` — `ClaimSpec.Lifetime` field on the claim spec.
- `code:foundation/persistence/claim_handles.go::ClaimHandleRow.HeldDurable` — the column.
- `code:foundation/persistence/postgres/claim_handles.go::SetHeldDurable` — the promote-to-durable write.
- `code:runtime/auto_terminal.go::CheckAndFireResolution` — auto-terminal honors the `lifetime` setting.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md` as the core enabler of the asset pattern. The default `subgraph` lifetime preserves the existing held-claim semantics (auto-terminal cleans up at subgraph completion); `durable` opts into asset semantics.
