---
decision: depguard-consumption-isolation
---

# Services module import surface

## Choice

The bundled services' shipped packages import the protocols module only — never the foundation module or the graph, runtime, or control layers — enforced by dependency lint.

## Rationale

Bundled services ship as standalone images implementing the public protocol contract; anything rimsky-internal in their import graph couples the shipped images to core internals and makes the protocol contract insufficient for external implementers. The lint is the primary guard, not defense in depth: the services module's manifest also requires the core modules for its own never-shipped test tree, so the module graph alone does not block the edge.

## Alternatives

- Letting shipped services reuse foundation primitives — rejected: rimsky-internal code rides into the service images, and a bundled service stops being a reference implementation of the public contract.
- Relying on the module graph alone — rejected: the manifest's test-tree requirements leave the forbidden edge representable, so only lint makes it fail mechanically.
