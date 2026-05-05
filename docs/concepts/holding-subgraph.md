---
concept: holding-subgraph
definition: |
  The set of nodes a held claim's lifetime spans: an acquirer and the directly-declared inheritors. Computed at template deploy from explicit `inherits:` declarations. When all members reach a terminal state, Rimsky fires the producer verb (commit on all-success, abandon on any-failure) and the claim ends.
proto_symbol: (none)
config_field: (none)
api_surface: GET /lock-holders/{claim_handle_id}/claim-holders
related: [claim, claim-handle, inheritance, claim-producer]
deprecated_terms: []
---

# Holding subgraph

## Definition

The set of nodes a held claim's lifetime spans: an acquirer and the directly-declared inheritors. Computed at template deploy from explicit `inherits:` declarations. When all members reach a terminal state, Rimsky fires the producer verb (commit on all-success, abandon on any-failure) and the claim ends.

## Why it exists

Sometimes a claim's value extends past the acquiring node's run. Two common patterns:

- A node opens a snapshot; multiple downstream nodes read from that exact snapshot.
- A node picks an item from a queue; downstream nodes must operate on that same item before it's released.

Rimsky models this with explicit inheritance. The acquiring node declares the claim; downstream nodes declare `inherits:` against it. The set of acquirer + inheritors is the holding subgraph. The claim handle stays open until *every* member has terminated, then the resolution fires automatically.

The aggregate-outcome rule keeps held-claim resolution simple:

- All members succeeded → `Commit`
- Any member failed → `Abandon`

There is no graph-author terminal designation — the held claim resolves automatically when the subgraph completes. There is exactly one resolution per held claim, and the rule is the rule. Rimsky does not orchestrate partial commits, partial rollbacks, or first-delete-wins reconciliations.

## How you encounter it

- **Templates**: `stores:` on the acquirer plus `inherits:` on downstream nodes.
- **Control API**: `GET /lock-holders/{claim_handle_id}/claim-holders` lists the subgraph members and their per-member states. The route returns `200 OK` with `{"holders": []}` when the holders list is empty (e.g. after resolution).
- **Observability**: dashboards show held claim handles distinctly from active ones, with the subgraph visualized.

## Consumer-visible guarantees

- Held-claim resolution is automatic, single, and aggregate-outcome-driven. Each held claim resolves exactly once, when its subgraph completes.
- A held claim handle survives its parent run's deletion until its automatic resolution explicitly removes it. The handle outlives its acquirer's bookkeeping by design — that's what makes it "held."
- Inheritance is direct only. Transitive inheritance through dependency chains is not supported; if you need a chain, declare `inherits:` at every link.

## Common mistakes

- Expecting a held claim to roll back partial work on `Abandon`. Rimsky tells the producer "the consumer of this claim failed"; the producer decides what to do with its own state per its own configuration. Rimsky does not orchestrate rollback.
- Relying on transitive `inherits:` to propagate a claim through a dep chain. Each inheriting node must explicitly declare `inherits:`.
- Treating the holding subgraph as a transactional unit. It's a lifetime-extension mechanism for one claim, not a multi-node transaction.

## See also

- [`claim.md`](claim.md)
- [`claim-handle.md`](claim-handle.md)
- [`inheritance.md`](inheritance.md)
- [`claim-producer.md`](claim-producer.md)
