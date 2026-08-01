---
concept: lineage-record
status: as-is
aliases: []
---

# Lineage record

## What it is

An append-only record in the lineage projection (see `concept:lineage`). Two kinds:

- **leaf-run record** — one per leaf-run terminal. Captures the computational unit: which run terminated, its position in the run tree, what triggered its frame, what it substituted from upstream, what claims it held, the executor and template that produced it, and how it ended.
- **claim-terminal record** — one per claim-handle terminal (commit, natural abandon, force-cancelled abandon). Captures the data-promotion unit: which claim handle resolved, its place in the claim tree, the producer that resolved it, the substrate identity it touched, and the disposition. The projection covers every claim-handle terminal so post-mortem queries can reconstruct natural-vs-force-cancelled abandon flows alongside commits.

## Boundaries

Owns: the per-kind record shape, the projection-write path. Does NOT own: the projection storage or query surface (lives in `concept:lineage`), the external-receiver emission (lives with the external-receiver subscriber). Adjacent: `concept:lineage`, `concept:claim-handle`, `concept:node-run`, `concept:auto-terminal`.

## Invariants

- Both kinds are append-only; no updates.
- Leaf-run records emit at the leaf-run terminal path, fired from the per-terminal handlers in the runtime.
- Claim-terminal records emit at the unified terminal-decision engine's forensics emit site, so every commit / abandon / force-cancelled resolution lands a record in the same shape.
- An outcome value is REQUIRED on claim-terminal records. The writer rejects an empty outcome — an abandon path that forgets to set it cannot silently produce a record marked as committed.
- Identity and substrate references are hashed, never carried as payload bytes: held-claim references carry the claim-scope-data hash, not the bytes themselves (algorithm choice lives with the lineage decision). Per the inertness discipline, inert bytes do not appear in lineage records; a record may still carry open structured metadata fields, currently unassigned.

## What each record captures

A **leaf-run record** captures the computational unit at a leaf-run terminal: the run's identity and place in the run tree, the frame trigger that started its cascade, the substitution sources it read from, the claims it held, the executor and template that produced it, and a terminal-kind discriminator marking which emission site fired the record. The discriminator is drawn from a small closed family covering the standard success path, the park terminal, the error path, and the sub-graph-caller's internal-cascade-fire emission. A pre-dispatch acquisition failure resolved via a pass-disposition error policy emits no leaf-run record (the resolution happens before there is a run to anchor it). Pass-through nodes — fan-out parents (acquire-phase split, executor skipped) and pure-cascade nodes (no executor declared, settled on cascade alone) — also emit no leaf-run record by design; lineage covers computational units, and a pass-through has no computation to cite.

Sub-graph callers produce two leaf-run records per dispatch — one at internal-cascade-fire time capturing the calling node's inputs as the absorbed entry terminal, and one at the post-aggregation terminal capturing the resolved outcome. Both keys share the calling run's identity; downstream consumers discriminate on the terminal-kind field. The external-receiver subscriber maps each leaf-run record to one external completion event, so a sub-graph caller emits two completion events at the same external run identifier — intentional, not a duplicate emission. External receivers that treat the completion event as a terminal-state signal branch on the terminal-kind discriminator.

A **claim-terminal record** captures the data-promotion unit at a claim-handle terminal: the handle's identity and place in the claim tree, the producer that resolved it, the substrate identity as a content hash together with the resolved substrate version, and the disposition outcome. The disposition outcome distinguishes commit from natural abandon from force-cancelled abandon; a cause discriminator further names which force-cancelled path produced the abandon (the sibling-cancel walker or the parent-abandon recursive descent) — a natural abandon carries no separate cause value, since the disposition outcome alone already marks it as abandoned.
