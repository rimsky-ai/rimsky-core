---
decision: fan-out-and-delegation-are-distinct-mechanisms
---

# Fan-out and sub-graph delegation are distinct mechanisms, not variants of one shape

## Choice

Fan-out and sub-graph delegation are documented and implemented as two distinct mechanisms. They share a thin run-side dispatch helper that creates the child run-scopes and child runs, called with structurally different inputs:

- **Fan-out** clones the calling node N times: N partitions × 1 child whose node id is the calling node's own id. Each clone runs as the same node-type, with the same executor, in its own child run-scope. Fan-in is N→1 through claim-handle aggregation under an author-chosen policy; per-partition attribute writebacks do NOT aggregate onto the parent's attribute bag.
- **Sub-graph delegation** substitutes into the absorbed entry (the calling node IS the entry) and dispatches the sub-graph's other internal nodes (exit plus any intermediates) as 1 partition × N children sharing one child run-scope. Fan-in is 1→1 through the designated exit node; the delegation settle path carries the exit's writeback verbatim onto the calling node's attribute row.

Settlement is intentionally split: the two settle primitives stay separate because their fan-in mechanisms are structurally different. A fan-out node may itself be a sub-graph delegate — composition layers them — but the fan-out machinery still clones the calling node; sub-graph dispatch happens layered atop each clone's own terminal-resolution.

## Rationale

The unified framing — delegation as "fan-out with N=1" behind a single dispatch + single settle primitive — does not hold. Delegation does not clone its calling node; it dispatches the sub-graph's distinct internal nodes. Settle cannot unify — the delegation settle carries a single exit's writeback and closes a run-scope; the fan-out settle updates a claim-handle holder set and runs an author-policy aggregation. These are not duplicated logic that should collapse; they are different operations.

Conflating the two also hides the no-attribute-aggregation property of fan-out: because all N clones share the same template node and attribute schema, any rimsky-side merge across them would collide on every key. Authors needing per-fan-out aggregation route it through the claim producer's data-processing protocol surface — not through an inferred attribute merger. Treating fan-out as "delegation with more N" obscures that this is the correct shape, not an omission.

The shared dispatch helper is real but thin. It accepts a partitions × children matrix because that matrix expresses both shapes uniformly; it does NOT mean the two shapes are variants of one operation. The runtime layer above holds the actual mechanism for each.

## Alternatives

Unify the two by collapsing settle into one primitive that switches on a "shape" discriminator — rejected. The two settle paths have almost no shared logic; a single function would be a long switch statement with two disjoint arms, not a real abstraction.

Frame delegation as fan-out's N=1 special case — rejected. Delegation dispatches distinct internal nodes rather than cloning the caller, so nothing real survives the framing; there is no decision left for it to carry.

Drop the umbrella `concept:child-execution` entirely and let `concept:fan-out` and `concept:delegation` stand alone — rejected. The umbrella concept still earns its place by naming the thin shared dispatch helper and the two settlement-mode names (carry, aggregate) so callers and reviewers can refer to the shapes by stable names. The decision here just clarifies that the two mechanisms are distinct, not that the umbrella is wrong.
