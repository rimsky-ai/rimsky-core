---
decision: depguard-foundation-purity
status: as-is
---

# Foundation module import surface

## Choice

Stdlib + the protocols module + chosen libs only; no graph, runtime, or control layer imports.

## Rationale

The foundation module provides primitives, not workflow shape.
