---
decision: depguard-runtime-purity
status: as-is
---

# Runtime layer import surface

## Choice

The foundation module + the graph layer + the protocols module; not the control layer.

## Rationale

The control layer is the operator surface, not a runtime dep.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
