---
decision: substitution-grammar-closed
---

# Substitution grammar is closed to cascade-shape tokens

## Choice

The substitution grammar is a closed enumeration of data-reference tokens; cascade-shape declaration lives on the cascade-edge surface, not on the read surface.

## Rationale

Separation of read access from cascade coupling — the substitution grammar carries data references only.

## Alternatives

- An open grammar admitting cascade-shape tokens, so a read declaration also creates or shapes a cascade edge — rejected: couples data access to cascade coupling, letting the edge set diverge from what the author wrote on the subscription surface.
