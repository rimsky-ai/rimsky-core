---
decision: depguard-runtime-purity
status: as-is
---

# Runtime layer import surface

## Choice

The runtime layer imports the foundation module, the graph layer, and the protocols module — never the control layer — enforced by dependency lint.

## Rationale

The control layer is the operator surface layered above the runtime; a back-import would invert the four-layer ordering and make the runtime depend on how it is operated.

## Alternatives

- Allowing runtime-to-control imports where convenient — rejected: collapses the boundary between the engine and its operator surface; the runtime must stand without it.
- An unenforced layering convention — rejected: prose boundaries drift; the dependency lint makes a violation fail mechanically.
