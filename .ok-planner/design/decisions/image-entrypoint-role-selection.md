---
decision: image-entrypoint-role-selection
status: as-is
---

# Single-binary multi-role entrypoint

## Choice

`rimsky-entrypoint` with no command → all roles; single role command → that role; migrate runs once per deployment, owner role determined by command.

## Rationale

One image, many topologies.

## Notes

2026-06-08 — Decision recorded via spec 2026-06-08-design-corpus-bootstrap.
