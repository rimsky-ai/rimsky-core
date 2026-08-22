---
decision: substitution-grammar-closed
---

# Substitution grammar is closed to cascade-shape tokens

## Choice

The substitution grammar is a closed enumeration of data-reference tokens plus an optional literal fallback. It admits no cascade-shape tokens and no function forms. Cascade-shape declaration lives on the cascade-edge surface, not on the read surface, and aggregation and transformation live in receiver executors.

## Rationale

Separation of read access from cascade coupling — the substitution grammar carries data references only.

Keeping function forms out holds the grammar to naming a value. An in-grammar function would move computation into the template, in a notation with no types and no way to test it, while receiver executors already do that work with both.

## Alternatives

- An open grammar admitting cascade-shape tokens, so a read declaration also creates or shapes a cascade edge — rejected: couples data access to cascade coupling, letting the edge set diverge from what the author wrote on the subscription surface.
- Function forms combining or transforming several sources inside one directive — rejected: it starts an expression language in the template for work receiver executors already do, and every function added to it becomes a compatibility surface.
