---
decision: image-entrypoint-role-selection
status: as-is
---

# Single-binary multi-role entrypoint

## Choice

The shared entrypoint binary with no command → all roles; single role command → that role; migrate runs once per deployment, owner role determined by command (see `concept:rimsky`).

## Rationale

One image, many topologies.
