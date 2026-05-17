---
concept: asset
status: as-is
aliases: []
references:
  - ../../specs/2026-05-15-data-platform-extensions-design.md
---

# Asset

## Definition

An asset is a documented compound, not a new primitive: a claim against a `DataProcessing`-capable producer with `lifetime: durable`. Anything satisfying both is an asset; anything else isn't. Rimsky does not apply asset semantics to other claims.

The asset presentation surface is a query alias over `rimsky_claim_handles` filtered by `held_durable = TRUE` joined against `DataProcessing`-advertising producers, augmented with `rimsky_lineage` walks and `DataProcessing.ListVersions` / `ListPartitions` / `GetVersionSchema` calls.

## Boundaries

Owns: the compound definition, the control-api `/instances/{id}/assets/...` endpoints (list, detail, versions, materialization-history, materialize, delete), the CLI subcommands (`rimsky-cli asset list/show/materialize/versions/delete`), the dashboard asset-primary panel. Does NOT own: any new primitive (assets are claims; see `concept:claim`, `concept:claim-lifetime`). Adjacent: `concept:claim-lifetime`, `concept:claim-handle`, `concept:data-processing`, `concept:lineage`.

## Invariants

- Per-instance namespacing: `{instance_id}.{asset_alias}` is the canonical identity for V1.
- The producer MUST advertise `data_processing` in `Capabilities.Protocols`. A claim with `lifetime: durable` against a non-`DataProcessing` producer is a held-durable claim, not an asset.
- The asset's `data:` block in the template is producer-targeted and opaque to rimsky. Rimsky-aware fields outside `data:`: `producer`, `scope`, `lifetime`, `write_semantics`.
- `DELETE /instances/{id}/assets/{alias}` calls `ClaimProducer.Release` on the claim handle; refuses if any in-flight run holds the claim.
- `POST /instances/{id}/assets/{alias}/materialize` is an alias for sending an invalidate-kind message targeting the asset's producer node.

## Annotation sites

- `code:control/controlapi/assets.go` — the `/instances/{id}/assets/...` route handlers.
- `code:control/cli/asset.go` — `rimsky-cli asset` subcommand group.
- `code:dashboards/rimsky-dashboard/src/assets/` — dashboard asset-primary panel.
- `code:runtime/instance_termination.go::ReleaseHeldDurableClaims` — instance-termination release path.
- `code:test/scenarios/asset/` — asset-pattern scenarios.

## Notes

Introduced by `.ok-planner/specs/2026-05-15-data-platform-extensions-design.md`. The asset thinking surface is intentionally a presentation alias over existing primitives — there's no `rimsky_assets` table, no special row type, no separate lifecycle. Producers handle the durable-storage substrate; rimsky just records "this claim is held-durable" and surfaces the join across `claim_handles + lineage + DataProcessing` as a coherent operator view.
