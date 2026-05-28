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

Owns: the compound definition, the control-api asset endpoints (list, detail, versions, materialization-history, materialize, delete), the matching CLI asset subcommands, the dashboard asset-primary panel. Does NOT own: any new primitive (assets are claims; see `concept:claim`, `concept:claim-lifetime`). Adjacent: `concept:claim-lifetime`, `concept:claim-handle`, `concept:data-processing`, `concept:lineage`.

## Invariants

- Per-instance namespacing: `{instance_id}.{asset_alias}` is the canonical identity for V1.
- The producer MUST advertise the data-processing capability. A durable-lifetime claim against a producer lacking that capability is a held-durable claim, not an asset.
- The asset's `data:` block in the template is producer-targeted and opaque to rimsky. Rimsky-aware fields outside `data:`: `producer`, `scope`, `lifetime`, `write_semantics`.
- The asset-delete endpoint releases the claim handle via the producer's release verb; it refuses if any in-flight run holds the claim.
- The asset-materialize endpoint is an alias for sending an invalidate-kind message targeting the asset's producer node.

## Notes

Introduced by spec:2026-05-15-data-platform-extensions-design. The asset thinking surface is intentionally a presentation alias over existing primitives — there's no asset table, no special row type, no separate lifecycle. Producers handle the durable-storage substrate; rimsky just records "this claim is committed and durable" and surfaces the join across claim handles, lineage, and the producer's data-processing listings as a coherent operator view.

State-column refactor per spec:2026-05-17-post-data-platform-cleanup: the asset query previously keyed off a dedicated held-durable flag; it now keys off the committed state plus a durable lifetime. Functionally identical; the post-refactor predicate exposes the underlying lifecycle clearly (the row is committed via auto-terminal Promote and survives because lifetime is durable, not because of a flag).

- 2026-05-25 — Codebase citations removed + cross-refs repaired for self-containment per spec:2026-05-25-concept-doc-self-containment.
