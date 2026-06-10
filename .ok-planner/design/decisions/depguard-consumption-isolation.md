---
decision: depguard-consumption-isolation
status: as-is
---

# Services module import surface

## Choice

The protocols module only; not the foundation module, graph layer, runtime layer, or control layer.

## Rationale

Bundled services ship as standalone images — defense in depth against rimsky-internal leakage.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
