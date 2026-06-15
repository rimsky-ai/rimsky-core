---
decision: licensing-enforced-by-license-lint
status: as-is
---

# License-import discipline

## Choice

Enforced by a build-step license check that constrains Apache-licensed packages to import only the standard library plus permissive-licensed and Apache-licensed dependencies (see `concept:module-layout`).

## Rationale

Prevent AGPL contamination of permissive consumers.
