---
decision: depguard-foundation-purity
---

# Foundation module import surface

## Choice

The foundation module imports only the stdlib, the protocols module, and its chosen third-party libraries — never the graph, runtime, or control layers — enforced by dependency lint.

## Rationale

The foundation module provides primitives, not workflow shape; an upward import would invert the four-layer ordering and entangle the primitives with the layers built on them.

## Alternatives

- Allowing upward imports where convenient — rejected: primitives entangled with workflow layers stop being primitives, and the four-layer ordering collapses.
- An unenforced layering convention — rejected: prose boundaries drift; the dependency lint makes a violation fail mechanically.
