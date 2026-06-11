---
decision: depguard-consumption-isolation
status: as-is
---

# Services module import surface

## Choice

The protocols module only; not the foundation module, graph layer, runtime layer, or control layer.

## Rationale

Bundled services ship as standalone images — defense in depth against rimsky-internal leakage.
