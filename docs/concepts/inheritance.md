---
concept: inheritance
definition: |
  The DSL mechanism by which a downstream node declares it will use a live claim from an upstream acquirer. Direct only — does not propagate transitively through dep chains. Inheriting nodes can substitute the inherited claim's address, payload, and scope into their own attributes.
proto_symbol: (none)
config_field: (none)
api_surface: (none)
related: [claim, holding-subgraph, attributes]
deprecated_terms: []
---

# Inheritance

## Definition

The DSL mechanism by which a downstream node declares it will use a live claim from an upstream acquirer. Direct only — does not propagate transitively through dep chains. Inheriting nodes can substitute the inherited claim's address, payload, and scope into their own attributes.

## Why it exists

Two very different propagation patterns coexist in Rimsky:

- **Value-pass**: a source node extracts captured fields into its own attributes; downstream nodes consume via `{{nodes.<source>.attribute.<field>}}`. Lifetime-independent — works after the source's claim has closed.
- **Claim-pass**: a downstream node inherits the live claim and uses `{{claim.<alias>.address | payload.<f> | scope}}`. Requires the claim to remain open; the inheriting node's existence holds it.

Inheritance enables claim-pass. Without it, every downstream consumer would need to re-acquire the same scope, with risk of getting a different snapshot or a different queue item.

The "direct only" rule is a deliberate constraint. Allowing transitive inheritance through arbitrary subscription chains would make claim lifetimes hard to reason about; with direct-only, you can read a template and immediately see which nodes hold a given claim.

## Propagation modes

The two propagation modes are distinct mechanisms, and a template can use both:

- **Value-pass**: source captures fields into its committed attributes; downstream `{{nodes.<source>.attribute.<field>}}` reads them. Survives after the source's claim closes.
- **Claim-pass**: downstream `inherits: [{ claim: <alias> }]` widens the holding subgraph; downstream `{{claim.<alias>.*}}` reads the live claim. Closes when the entire subgraph terminates. Each entry references a claim alias declared in an upstream node's `stores:` block; the upstream node must be reachable from this node via the subscription graph.

## How you encounter it

- **Templates**: the `inherits:` block on a node declaration.
- **Substitution**: in inheriting nodes, `{{claim.<alias>.address}}`, `{{claim.<alias>.payload.<field>}}`, `{{claim.<alias>.scope}}` resolve against the inherited claim's stored values.

## Consumer-visible guarantees

- Inheritance is direct only. A node that wants to inherit a claim must explicitly name the claim's alias in its own `inherits:` block; transitive inheritance through subscription chains is not supported.
- An inheriting node's existence widens the holding subgraph by one member. The claim is held until every member terminates.

## Common mistakes

- Confusing inheritance with object-oriented inheritance. There are no methods or fields being inherited from a parent class; this is a directive that says "when I run, the claim with this alias is still live and available for me to substitute."
- Expecting `{{nodes.<node>.attribute.<field>}}` and `{{claim.<alias>.*}}` to be interchangeable. The first reads from the upstream node's persisted attributes (works after the claim has closed); the second reads from the live claim (requires inheritance to keep the claim open).

## See also

- [`claim.md`](claim.md)
- [`holding-subgraph.md`](holding-subgraph.md)
- [`attributes.md`](attributes.md)
