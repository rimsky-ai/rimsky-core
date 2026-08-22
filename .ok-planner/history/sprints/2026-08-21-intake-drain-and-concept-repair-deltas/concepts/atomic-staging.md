---
concept: atomic-staging
---

# Atomic staging

## What it is

Atomic staging is the producer-side stage-then-swap discipline behind a held claim. Writers stage their data into a side area. When the claim commits, the producer swaps the staging into the canonical view in one step. When the claim abandons, the producer drops the staging and leaves the canonical view untouched. It realizes the staged-asynchronous mode of `concept:write-semantics`: a reader sees the pre-stage snapshot until a swap lands. It composes with a subgraph-lifetime claim, co-holding verifier nodes, and aggregation — the claim's auto-terminal decides commit or abandon from the aggregate of its holders, and a verifier node that co-holds the staging claim contributes its own terminal to that aggregate.

## Purpose

Atomic staging lets a group of nodes write a dataset together and expose it to readers as one change. A reader never observes a half-written canonical view: it reads the previous snapshot until the swap lands, and the swap is the moment the new data becomes visible. A failure anywhere in the writing group leaves the canonical view exactly as it was.

## Boundaries

Atomic staging owns the producer-side discipline. Whether a given substrate can perform the swap in one step is the producer's concern, not rimsky's. Atomic staging does not own the rimsky-side mechanics: the subgraph-lifetime claim, co-holdership, and aggregation are each their own concept. It does not own the substrate, which belongs to the producer and which rimsky does not interpret.

see also: `claim-producer`, `claim-lifetime`, `claim-co-holdership`, `auto-terminal`, `write-semantics`
