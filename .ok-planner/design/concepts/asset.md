---
concept: asset
status: as-is
aliases: []
---

# Asset

## Definition

An asset is a documented compound, not a new primitive: a claim against a data-processing-capable producer with a durable lifetime. Anything satisfying both is an asset; anything else isn't. Rimsky does not apply asset semantics to other claims.

The asset presentation surface is a query alias over the claim-handle ledger filtered to committed handles with a durable lifetime, joined against data-processing-advertising producers, augmented with lineage walks and the producer's version/partition/schema listing calls. The accessor lists an instance's claim handles by committed-and-durable state.

## Boundaries

Owns: the compound definition, the control-api asset endpoints (list, detail, versions, materialization-history, delete), the matching CLI asset subcommands, the dashboard asset-primary panel. Does NOT own: any new primitive (assets are claims; see `concept:claim`, `concept:claim-lifetime`); re-materialization triggering (operators express re-materialization via messages — empty for whole-instance, typed for template-author-designed partial paths). Adjacent: `concept:claim-lifetime`, `concept:claim-handle`, `concept:data-processing`, `concept:lineage`.

## Invariants

- Per-instance namespacing: `{instance_id}.{asset_alias}` is the canonical identity.
- The producer MUST advertise the data-processing capability. A durable-lifetime claim against a producer lacking that capability is a held-durable claim, not an asset.
- The asset's `data:` block in the template is producer-targeted and opaque to rimsky. Rimsky-aware fields outside `data:`: `producer`, `scope`, `lifetime`, `write_semantics`.
- The asset-delete endpoint releases the claim handle via the producer's release verb; it refuses if any in-flight run holds the claim.
