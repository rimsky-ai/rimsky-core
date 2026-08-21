---
concept: claim-co-holdership
---

# Claim co-holdership

## What it is

Co-holdership is several node-runs holding one claim. A template names, on a downstream node, an upstream node and one of the claims that upstream node acquires. The downstream node then joins that claim as a co-holder instead of opening a claim of its own, and may refer to the joined claim under a local name of its choosing. Co-holdership extends the holding subgraph — the set of runs one claim's life spans — to cover the acquirer and every co-holder, so the claim settles only after every one of those runs leaves the active state (see `concept:claim`, `concept:auto-terminal`).

## Purpose

Co-holdership lets a downstream node read a live claim instead of acquiring the same resource again. Without it, every downstream consumer would open its own claim on the same scope and risk a different snapshot or a different queue item.

Two propagation patterns then coexist in one template:

- **Value-pass.** A source node copies what it captured into its own attributes, and downstream nodes read those attributes. The claim may already have closed, so the pattern is independent of the claim's life and needs no co-hold declaration.
- **Claim-pass.** A downstream node co-holds the live claim and reads its address, its scope, and its payload directly. The claim must stay open, so every co-holder widens the holding subgraph and extends the claim's life.

## Boundaries

Co-holdership owns the co-hold declaration a template makes and the extension of the holding subgraph over co-holders. It does not own acquisition, which belongs to `claim`, or the aggregation of member state in the parent run, which belongs to `node-run`. A co-holder joins only a claim the named node acquires itself: co-holdership does not chain, so a node whose upstream is itself a co-holder cannot reach the claim through that upstream. Subscribing to a node confers no co-holdership; a node co-holds only where the template says so. See also: `claim`, `claim-handle`, `auto-terminal`, `node-run`, `cancel-siblings`.
