---
concept: claim-co-holdership
status: as-is
aliases: []
---

# Claim co-holdership

## Definition

Multiple node-runs holding the same claim handle via the co-hold directive. Distinct from the claim-acquisition directive: the co-hold form adds a co-holder row against an existing handle rather than opening a new one. The co-holdership extends the holding subgraph — auto-terminal fires only after every co-holder row for the handle is non-active.

Template shape: a downstream node that subscribes to an upstream terminal carries a co-hold block whose entries are keyed by the claim alias the upstream node declared, identifying which of the upstream's claims to co-hold; an optional rename lets the co-holder refer to the held claim under a different local name in its own substitutions. The directive sits alongside the node's other declarations (its executor, its subscriptions, its attribute payload) and is what distinguishes a co-holder from a fresh acquirer.

Co-holdership enables two distinct propagation patterns to coexist in a template:

- **Value-pass.** A source node extracts captured fields into its own attributes; downstream nodes consume them via the substitution grammar's upstream-node source kind. Lifetime-independent — works after the source's claim has closed. No co-hold declaration needed.
- **Claim-pass.** A downstream node co-holds the live claim and consumes its address, scope, and payload fields via the substitution grammar's co-held claim source kind. Requires the claim to remain open; every co-holder's existence widens the holding subgraph and extends the claim's lifetime.

Without claim-pass, every downstream consumer would need to re-acquire the same scope, risking a different snapshot or a different queue item. The "from an upstream dependency" rule (see Invariants) is the deliberate constraint that keeps claim lifetimes legible: reading a template, you can immediately see which runs hold a given claim. There is no transitive auto-holdership through subscription chains; if you need a chain, declare co-holdership at every link.

## Boundaries

Owns: the `holds:` template directive, the per-co-holder row insertion at the co-holder's own acquire transaction, the holding-subgraph extension over co-holders. Does NOT own: claim acquisition (see `concept:claim`), state aggregation in the parent run (see `concept:node-run`). Adjacent: `concept:claim`, `concept:claim-handle`, `concept:auto-terminal`, `concept:node-run`.

## Invariants

- A co-holdership `from:` pointer MUST reference an upstream dependency. The co-holdership edges are a subset of `concept:graph`'s node-dependency edges and naturally acyclic.
- At dispatch, the co-holder's execution request carries the co-held claim's address (the same acquired result the original acquirer received) — same wire shape as a fresh acquire. Per invariant 20 the bytes are inert in rimsky.
- Persistence: the co-holder row is inserted in the co-holder's own acquire transaction, keyed by the holder run.
- Auto-terminal fires only after every co-holder row for the claim handle is non-active, subject to the additional firing conditions `concept:auto-terminal` owns. The holding-subgraph extension includes the acquirer plus every co-holder.
- Multiple co-holders are supported — the `holds:` block can list many; multiple nodes can co-hold the same claim independently. When any co-holder fails, `concept:auto-terminal`'s poison rule drives every participating holder to failed and fires Abandon at that resolution moment. This is distinct from `concept:cancel-siblings`, which walks a fan-out parent's sibling sub-claims under a strict aggregation policy, not co-holder rows.
