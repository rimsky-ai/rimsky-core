---
decision: structural-root-edges-derived-on-demand
---

# Structural-root edges are derived on demand with the rest of the inverse-edge map

## Choice

The runtime derives a template's inverse-edge map on demand from the stored spec and memoizes it per template hash. The builder that derives the map adds one synthetic entry per structural-root receiver, waking on a success terminal without forcing upstream refresh. The synthetic entry lives on the derived map only; the canonical template hash is over the spec bytes and is unaffected. A structural root is a main-graph node with no upstream of any kind: no non-self subscribes entries, no upstream attribute substitution refs, and no message-body consumption. A self-subscription (node == self) does not establish an upstream and is excluded from the disqualification. A substitution ref and a message-body read each disqualify a node from being a structural root. Neither contributes an edge to the map (see `decision:subscription-edges-only-from-explicit-block`). A node declared inside a sub-graph never gets the synthetic entry: a caller reaches a sub-graph through its entry node, which the calling node absorbs (see `concept:sub-graph`).

## Rationale

Structural roots are fully template-determinable: per-instance variation in attribute overrides, params, or service bindings does not change subscription topology, so per-instance storage of the augmented edges would be gratuitous redundancy. Adding the entry inside the map builder puts it where every consumer already goes. The runtime builds the map as it walks a cascade, the command-line client builds it to decide whether to send a wake message, and the test harness builds it too. Each consumer therefore gets the same map from the spec it holds, and none needs a second step. Memoizing per template hash pays the derivation once per template. Restricting roots to the main graph keeps the empty message's wake surface to the nodes an operator addresses directly (per `story:empty-message-wakes-roots`). A sub-graph runs when its caller delegates to it.

## Alternatives

- Build the map at template registration and store the result — rejected: a consumer outside the registering process holds only the spec and would derive the map itself anyway, so registration would become a second construction path to keep in step.
- Per-instance overlay (new column or table) — rejected: gratuitous redundancy across instances of the same template.
- Special-case the synthetic sender in the cascade walker instead of putting its edge in the map — rejected: a branch for one sender in the walker contradicts the uniformity property the map exists to hold.
