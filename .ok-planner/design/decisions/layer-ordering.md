---
decision: layer-ordering
status: as-is
---

# Four-layer ordered code split under the root module

## Choice

Within the root module, four ordered layers: the foundation module's layer → the graph layer → the runtime layer → the control layer, enforced by depguard.

## Rationale

Directed dependency DAG; lower layers never see higher ones.

## Alternatives

- A flat, unlayered root module with dependency direction left to convention — rejected: boundaries drift without a mechanical check; the lint is what keeps lower layers from reaching up.
- One Go module per layer, using the module graph as the enforcement — rejected: multiplies go.mod/versioning overhead for a boundary depguard enforces at zero release cost.
