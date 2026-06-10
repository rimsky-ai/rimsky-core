---
decision: layer-ordering
status: as-is
---

# Four-layer ordered code split under the root module

## Choice

Within the root module, four ordered layers: the foundation module's layer → the graph layer → the runtime layer → the control layer, enforced by depguard.

## Rationale

Directed dependency DAG; lower layers never see higher ones.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
