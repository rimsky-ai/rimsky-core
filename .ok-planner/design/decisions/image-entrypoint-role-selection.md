---
decision: image-entrypoint-role-selection
status: as-is
---

# Single-binary multi-role entrypoint

## Choice

The shared entrypoint binary with no command → all roles; single role command → that role; migrate runs once per deployment, owner role determined by command (see `concept:rimsky`).

## Rationale

One image, many topologies.

## Alternatives

- One image per role binary — rejected: multiplies the build and publish surface for binaries that ship together anyway.
- A mandatory dedicated migration job in every topology — rejected: forces an extra deployment step on the smallest setups; deriving migrate ownership from role selection covers single-container and split topologies without racing or missing runs.
