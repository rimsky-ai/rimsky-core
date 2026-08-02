---
decision: image-entrypoint-role-selection
---

# Single-binary multi-role entrypoint

## Choice

The shared entrypoint binary with no command → all roles; a single role command → that role; any other command exits non-zero with an error naming the value. Migrate runs once per deployment, owner role determined by command, and an invocation that owns migration runs it synchronously to completion before starting any role — in all-in-one and split-role topologies alike (see `concept:rimsky`).

## Rationale

One image, many topologies. Failing loud on an unknown command keeps a typo'd role name from silently running the wrong topology, and migrate-before-roles means no role ever sees a half-migrated schema.

## Alternatives

- One image per role binary — rejected: multiplies the build and publish surface for binaries that ship together anyway.
- A mandatory dedicated migration job in every topology — rejected: forces an extra deployment step on the smallest setups; deriving migrate ownership from role selection covers single-container and split topologies without racing or missing runs.
