---
concept: asset
status: as-is
aliases: []
---

# Asset

## Definition

An asset is a documented compound, not a new primitive: a committed claim against a data-processing-capable producer with a durable lifetime. Anything satisfying all three is an asset; anything else isn't. Rimsky does not apply asset semantics to other claims.

Assets are surfaced by querying claim handles that are committed and durable against data-processing-advertising producers.

## Boundaries

Owns: the compound definition, the asset presentation surface (listing, detail, versions, materialization-history, delete operations across operator interfaces). Does NOT own: any new primitive (assets are claims; see `concept:claim`, `concept:claim-lifetime`); re-materialization triggering (operators express re-materialization via messages — empty for whole-instance, typed for template-author-designed partial paths). Adjacent: `concept:claim-lifetime`, `concept:claim-handle`, `concept:data-processing`, `concept:lineage`.

## Invariants

- Assets are namespaced per-instance; the identity composes the instance with the asset's alias.
- The producer MUST advertise the data-processing capability. A durable-lifetime claim against a producer lacking that capability remains durable but is not surfaced as an asset (see `concept:claim-lifetime`).
- The asset's `data:` block in the template is producer-targeted and opaque to rimsky. Rimsky-aware fields outside `data:`: `name`, `selector`, `intent`, `alias`, `lifetime`.
- The asset-delete endpoint releases the claim handle via the producer's release verb; it refuses if any in-flight run holds the claim.
